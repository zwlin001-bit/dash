package cloudmetric

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/provider"
)

func init() {
	events.RegisterType(events.TypeDef{
		Type:               "cloud.metric.sync_error",
		DisplayName:        "云监控指标拉取失败",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store+ui",
		Description:        "云厂商监控指标或流量拉取异常",
		IsBuiltin:          true,
	})
}

// Syncer handles periodic and on-demand metric synchronization.
type Syncer struct {
	db        *db.DB
	store     *Store
	credStore *credentials.Store
	pm        *provider.Manager
}

// NewSyncer creates a new Syncer instance.
func NewSyncer(d *db.DB, store *Store, credStore *credentials.Store, pm *provider.Manager) *Syncer {
	return &Syncer{
		db:        d,
		store:     store,
		credStore: credStore,
		pm:        pm,
	}
}

// FindGuardedInstances returns instances that are protected by enabled guard rules.
// Rule constraint: Only pull instances that have active guard rules.
func (s *Syncer) FindGuardedInstances(ctx context.Context) ([]GuardedInstance, error) {
	q := `SELECT r.id, r.cloud_account_id, r.res_ref, r.region, a.credential_id
		FROM cloud_resources r
		JOIN guard_rules g ON r.id = g.cloud_resource_id
		JOIN cloud_accounts a ON r.cloud_account_id = a.id
		WHERE g.is_enabled = 1 AND a.is_enabled = 1 AND r.is_deleted = 0`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []GuardedInstance
	for rows.Next() {
		var gi GuardedInstance
		var region sql.NullString
		if err := rows.Scan(&gi.ResourceID, &gi.CloudAccountID, &gi.ResRef, &region, &gi.CredentialID); err != nil {
			return nil, err
		}
		if region.Valid {
			gi.Region = region.String
		}
		list = append(list, gi)
	}
	return list, rows.Err()
}

// FindAccountTargets returns all enabled cloud accounts for CDT traffic sync.
func (s *Syncer) FindAccountTargets(ctx context.Context) ([]AccountSyncTarget, error) {
	q := `SELECT id, credential_id, default_region FROM cloud_accounts WHERE is_enabled = 1`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []AccountSyncTarget
	for rows.Next() {
		var ast AccountSyncTarget
		var defRegion sql.NullString
		if err := rows.Scan(&ast.CloudAccountID, &ast.CredentialID, &defRegion); err != nil {
			return nil, err
		}
		if defRegion.Valid {
			ast.DefaultRegion = defRegion.String
		}
		list = append(list, ast)
	}
	return list, rows.Err()
}

func (s *Syncer) resolveClientAndCred(ctx context.Context, credID string) (map[string]string, provider.ProviderClient, error) {
	_, pt, err := s.credStore.GetDecrypted(ctx, credID)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt credential %s: %w", credID, err)
	}

	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		return nil, nil, fmt.Errorf("parse credential payload: %w", err)
	}

	client, err := s.pm.GetClient(ctx, "aliyun")
	if err != nil {
		return nil, nil, fmt.Errorf("get provider client aliyun: %w", err)
	}

	return credMap, client, nil
}

// SyncOnce executes a single round of cloud metric sync.
// Policy:
// 1. Only pull guarded instances + account-level CDT traffic.
// 2. Incremental: get last_at_ms, only pull subsequent points.
// 3. Serial + backoff: failures log error/emit event, never block other resources.
func (s *Syncer) SyncOnce(ctx context.Context) (*SyncReport, error) {
	startTime := time.Now()
	report := &SyncReport{}
	nowMs := startTime.UnixMilli()

	// 1. Account-level CDT traffic
	accTargets, err := s.FindAccountTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("find account targets: %w", err)
	}

	for _, acc := range accTargets {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}

		credMap, client, err := s.resolveClientAndCred(ctx, acc.CredentialID)
		if err != nil {
			logx.Warn("cloud metric: resolve credential/client failed for account", "account_id", acc.CloudAccountID, "err", logx.Redact(err.Error()))
			report.ErrorsCount++
			continue
		}

		res, err := client.ListMetrics(ctx, provider.MetricListParams{
			Credential:  credMap,
			Region:      acc.DefaultRegion,
			ResRef:      "_account",
			MetricCode:  "traffic_month_up",
			StartTimeMs: 0,
			EndTimeMs:   nowMs,
			PeriodSec:   300,
		})
		if err != nil {
			logx.Warn("cloud metric: list cdt traffic failed", "account_id", acc.CloudAccountID, "err", logx.Redact(err.Error()))
			events.Emit(ctx, events.Event{
				Type:       "cloud.metric.sync_error",
				Source:     "cloudmetric",
				TargetKind: "account",
				TargetID:   acc.CloudAccountID,
				Title:      "云侧 CDT 流量拉取失败",
				Payload: map[string]any{
					"account_id": acc.CloudAccountID,
					"error":      logx.Redact(err.Error()),
				},
				OccurredAt: nowMs,
			})
			report.ErrorsCount++
		} else if res != nil && len(res.Points) > 0 {
			if err := s.store.SavePoints(ctx, acc.CloudAccountID, "_account", "traffic_month_up", res.Points); err != nil {
				logx.Warn("cloud metric: save cdt points failed", "account_id", acc.CloudAccountID, "err", err)
				report.ErrorsCount++
			} else {
				report.PointsSaved += len(res.Points)
			}
		}
		report.SyncedAccounts++
		time.Sleep(50 * time.Millisecond)
	}

	// 2. Guarded instances
	guardedInsts, err := s.FindGuardedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("find guarded instances: %w", err)
	}

	metricCodes := []string{"cpu_pct", "net_up_bps", "net_down_bps"}

	for _, inst := range guardedInsts {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}

		credMap, client, err := s.resolveClientAndCred(ctx, inst.CredentialID)
		if err != nil {
			logx.Warn("cloud metric: resolve cred/client failed for instance", "res_ref", inst.ResRef, "err", logx.Redact(err.Error()))
			report.ErrorsCount++
			continue
		}

		instanceHadError := false
		for _, code := range metricCodes {
			lastTs, err := s.store.GetLastTs(ctx, inst.CloudAccountID, inst.ResRef, code)
			if err != nil {
				lastTs = 0
			}

			var startMs int64
			if lastTs > 0 {
				startMs = lastTs + 1
			} else {
				startMs = nowMs - 6*3600*1000 // default initial 6 hours
			}

			if startMs >= nowMs {
				continue
			}

			res, err := client.ListMetrics(ctx, provider.MetricListParams{
				Credential:  credMap,
				Region:      inst.Region,
				Kind:        "ecs",
				ResRef:      inst.ResRef,
				MetricCode:  code,
				StartTimeMs: startMs,
				EndTimeMs:   nowMs,
				PeriodSec:   300,
			})
			if err != nil {
				logx.Warn("cloud metric: list instance metric failed", "res_ref", inst.ResRef, "code", code, "err", logx.Redact(err.Error()))
				if !instanceHadError {
					instanceHadError = true
					events.Emit(ctx, events.Event{
						Type:       "cloud.metric.sync_error",
						Source:     "cloudmetric",
						TargetKind: "resource",
						TargetID:   inst.ResourceID,
						Title:      fmt.Sprintf("实例 %s 云监控指标拉取失败", inst.ResRef),
						Payload: map[string]any{
							"res_ref": inst.ResRef,
							"code":    code,
							"error":   logx.Redact(err.Error()),
						},
						OccurredAt: nowMs,
					})
				}
				report.ErrorsCount++
				time.Sleep(100 * time.Millisecond) // backoff
				continue
			}

			if res != nil && len(res.Points) > 0 {
				if err := s.store.SavePoints(ctx, inst.CloudAccountID, inst.ResRef, code, res.Points); err != nil {
					logx.Warn("cloud metric: save instance points failed", "res_ref", inst.ResRef, "code", code, "err", err)
					report.ErrorsCount++
				} else {
					report.PointsSaved += len(res.Points)
				}
			}

			time.Sleep(50 * time.Millisecond)
		}

		report.SyncedInstances++
	}

	report.DurationMs = time.Since(startTime).Milliseconds()
	return report, nil
}
