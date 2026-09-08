package guard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dash/internal/audit"
	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/jobs"
	"dash/internal/logx"
	"dash/internal/provider"
	"dash/internal/ulid"
)

type Config struct {
	Interval        time.Duration `json:"interval"` // 评估周期，默认 60s
	DryRun          bool          `json:"dry_run"`
	PrewarnRatio    float64       `json:"prewarn_ratio"` // 默认 0.8
	DefaultTimezone string        `json:"default_timezone"`
}

type Engine struct {
	db          *db.DB
	store       *Store
	credStore   *credentials.Store
	providerMgr *provider.Manager
	jobEngine   *jobs.Engine
	cfg         Config

	evalMu             sync.Mutex
	lastObservedMonths map[string]string  // account_id -> "2026-09"
	lastCDTTraffic     map[string]float64 // account_id -> last cdt used gb

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine 创建并初始化守卫引擎。
func NewEngine(d *db.DB, store *Store, credStore *credentials.Store, pm *provider.Manager, je *jobs.Engine, cfg Config) *Engine {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.PrewarnRatio <= 0 {
		cfg.PrewarnRatio = 0.8
	}
	if cfg.DefaultTimezone == "" {
		cfg.DefaultTimezone = "Asia/Shanghai"
	}

	e := &Engine{
		db:                 d,
		store:              store,
		credStore:          credStore,
		providerMgr:        pm,
		jobEngine:          je,
		cfg:                cfg,
		lastObservedMonths: make(map[string]string),
		lastCDTTraffic:     make(map[string]float64),
	}

	e.registerEventTypes()
	return e
}

// Store 返回关联的守卫持久化存储。
func (e *Engine) Store() *Store {
	return e.store
}

// registerEventTypes 注册 P2-04 §4 规定的事件类型。
func (e *Engine) registerEventTypes() {
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.traffic_warning",
		DisplayName:        "CDT流量超额预警",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store+notify",
		Description:        "阿里云账号 CDT 流量用量达到阈值的 80% 预警线",
	})
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.instance_stopped",
		DisplayName:        "ECS实例已自动停止",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store+notify",
		Description:        "ECS 实例因流量超额或计划日程被自动关机",
	})
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.instance_started",
		DisplayName:        "ECS实例已自动启动",
		DefaultSeverity:    "info",
		DefaultDisposition: "store+ui",
		Description:        "ECS 实例因保活策略或计划日程被自动开机",
	})
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.action_failed",
		DisplayName:        "ECS守卫动作执行失败",
		DefaultSeverity:    "critical",
		DefaultDisposition: "store+notify",
		Description:        "ECS 启停操作在云 API 调用或状态确认阶段发生错误",
	})
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.traffic_reset",
		DisplayName:        "CDT流量月初重置",
		DefaultSeverity:    "info",
		DefaultDisposition: "store+ui",
		Description:        "新月份开始，CDT 流量已清零并完成受控实例补检",
	})
	events.RegisterType(events.TypeDef{
		Type:               "cloud.guard.cycle_error",
		DisplayName:        "守卫评估周期异常",
		DefaultSeverity:    "critical",
		DefaultDisposition: "store+notify",
		Description:        "CDT 流量查询失败或云资源拉取失败，本轮已暂停启动以保障安全",
	})
}

// Start 启动后台守护评估循环。
func (e *Engine) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)

	e.wg.Add(1)
	go e.loop()

	logx.Info("guard engine started", "interval", e.cfg.Interval)
	return nil
}

// Stop 优雅停止守卫引擎。
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	logx.Info("guard engine stopped")
}

func (e *Engine) loop() {
	defer e.wg.Done()

	// 启动后先进行首次评估
	select {
	case <-e.ctx.Done():
		return
	case <-time.After(2 * time.Second):
		_, _ = e.EvaluateOnce(e.ctx, false)
	}

	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			_, _ = e.EvaluateOnce(e.ctx, false)
		}
	}
}

// EvaluateOnce 执行一轮完整评估（支持 dry-run 演练模式）。
func (e *Engine) EvaluateOnce(ctx context.Context, dryRun bool) (*EvaluateResult, error) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()

	startMs := time.Now().UnixMilli()
	now := time.Now()

	result := &EvaluateResult{
		CycleID:     ulid.New(),
		StartedAtMs: startMs,
		IsDryRun:    dryRun,
	}

	// 1. 查询全部活跃云账号
	accounts, err := e.loadActiveAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("guard: load active accounts: %w", err)
	}

	// 2. 加载全部规则映射
	rulesMap, err := e.store.ListRules(ctx)
	if err != nil {
		logx.Warn("guard: list rules failed, proceeding with defaults", "err", err)
		rulesMap = make(map[string]*GuardRule)
	}

	for _, acc := range accounts {
		e.evaluateAccount(ctx, acc, rulesMap, now, dryRun, result)
	}

	result.DurationMs = int(time.Now().UnixMilli() - startMs)
	return result, nil
}

func (e *Engine) loadActiveAccounts(ctx context.Context) ([]cloud.CloudAccount, error) {
	q := `SELECT id, provider_code, name, credential_id, default_region, account_site, config_json, is_enabled, last_sync_at_ms, created_at_ms, updated_at_ms
FROM cloud_accounts WHERE is_enabled = 1`
	rows, err := e.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accs []cloud.CloudAccount
	for rows.Next() {
		var a cloud.CloudAccount
		var defReg, site, cfgJSON sql.NullString
		var lastSync sql.NullInt64
		var isEn int
		if err := rows.Scan(&a.ID, &a.ProviderCode, &a.Name, &a.CredentialID, &defReg, &site, &cfgJSON, &isEn, &lastSync, &a.CreatedAtMs, &a.UpdatedAtMs); err != nil {
			return nil, err
		}
		a.IsEnabled = isEn == 1
		if defReg.Valid {
			a.DefaultRegion = defReg.String
		}
		if site.Valid {
			a.AccountSite = site.String
		}
		if cfgJSON.Valid {
			a.ConfigJSON = cfgJSON.String
		}
		if lastSync.Valid {
			a.LastSyncAtMs = lastSync.Int64
		}
		accs = append(accs, a)
	}
	return accs, rows.Err()
}

func (e *Engine) evaluateAccount(ctx context.Context, acc cloud.CloudAccount, rulesMap map[string]*GuardRule, now time.Time, dryRun bool, result *EvaluateResult) {
	cycleStartMs := time.Now().UnixMilli()

	// 查取解密凭据
	_, pt, err := e.credStore.GetDecrypted(ctx, acc.CredentialID)
	if err != nil {
		logx.Error("guard: decrypt credential failed for account", "account_id", acc.ID, "err", err)
		return
	}
	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		logx.Error("guard: parse credential failed for account", "account_id", acc.ID, "err", err)
		return
	}

	client, err := e.providerMgr.GetClient(ctx, acc.ProviderCode)
	if err != nil {
		logx.Error("guard: get provider client failed", "provider", acc.ProviderCode, "err", err)
		return
	}

	// 账号所在时区与月份键
	loc := LoadLocationWithFallback(e.cfg.DefaultTimezone)
	nowInLoc := now.In(loc)
	monthKey := nowInLoc.Format("2006-01")

	// 查取账号 CDT 流量 (每账号每轮只查 1 次)
	var cdtUsedGB float64
	var cdtErr error
	cdtRes, err := client.GetCDTTraffic(ctx, credMap)
	if err != nil {
		cdtErr = err
		logx.Warn("guard: CDT traffic query failed", "account", acc.Name, "err", logx.Redact(err.Error()))
		// 触发 cycle_error 事件
		events.Emit(ctx, events.Event{
			Type:       "cloud.guard.cycle_error",
			Source:     "guard",
			TargetKind: "cloud_account",
			TargetID:   acc.ID,
			Title:      fmt.Sprintf("CDT 流量查询异常: %s", acc.Name),
			Payload: map[string]any{
				"account_name": acc.Name,
				"error_type":   "CDT流量查询失败",
				"error":        logx.Redact(err.Error()),
			},
			DedupKey: fmt.Sprintf("guard:%s:cycle_error:%s", acc.ID, monthKey),
		})
	} else if cdtRes != nil {
		cdtUsedGB = float64(cdtRes.TrafficBytes) / (1024 * 1024 * 1024)
	}

	// 月初流量重置检测 (P2-04 §3)
	lastMonth, hasSeenMonth := e.lastObservedMonths[acc.ID]
	if hasSeenMonth && lastMonth != monthKey {
		// 跨月触发补检
		lastUsage := e.lastCDTTraffic[acc.ID]
		// 若刚过0点，CDT可能延时未清零；若读数明显下降（或<前值50%或接近0），确认已重置
		if cdtUsedGB < lastUsage || cdtUsedGB < 0.5 || nowInLoc.Minute() >= 30 {
			e.lastObservedMonths[acc.ID] = monthKey
			logx.Info("guard: detected month reset", "account", acc.Name, "month", monthKey, "new_traffic_gb", cdtUsedGB)
			events.Emit(ctx, events.Event{
				Type:       "cloud.guard.traffic_reset",
				Source:     "guard",
				TargetKind: "cloud_account",
				TargetID:   acc.ID,
				Title:      fmt.Sprintf("阿里云 CDT 流量已月初清零 (%s)", monthKey),
				Payload: map[string]any{
					"account_name": acc.Name,
					"month_key":    monthKey,
					"cdt_used_gb":  cdtUsedGB,
				},
				DedupKey: fmt.Sprintf("guard:%s:traffic_reset:%s", acc.ID, monthKey),
			})
		}
	} else if !hasSeenMonth {
		e.lastObservedMonths[acc.ID] = monthKey
	}
	e.lastCDTTraffic[acc.ID] = cdtUsedGB

	// 查取本账号下全部 ECS 资源
	instances, err := e.loadAccountInstances(ctx, acc.ID)
	if err != nil {
		logx.Error("guard: load account instances failed", "account_id", acc.ID, "err", err)
		return
	}

	// 按 region 分组，批量查 ECS 状态 (DescribeInstances 按 region 批查，每批 ≤ 100)
	regionGroups := make(map[string][]cloud.CloudResource)
	for _, inst := range instances {
		reg := inst.Region
		if reg == "" {
			reg = acc.DefaultRegion
		}
		if reg == "" {
			reg = "cn-hangzhou"
		}
		regionGroups[reg] = append(regionGroups[reg], inst)
	}

	// 逐 region 批查最新状态
	liveStatusMap := make(map[string]string) // res_ref -> status
	for reg := range regionGroups {
		cloudResList, err := client.ListResources(ctx, credMap, reg, "ecs", acc.AccountSite)
		if err != nil {
			logx.Warn("guard: batch query ECS status failed for region", "account", acc.Name, "region", reg, "err", logx.Redact(err.Error()))
			continue
		}
		for _, cr := range cloudResList {
			liveStatusMap[cr.Ref] = cr.Status
		}
	}

	cycleEvaluated := 0
	cycleActed := 0
	cycleFailed := 0

	// 逐实例决策
	for _, inst := range instances {
		if liveStatus, ok := liveStatusMap[inst.ResRef]; ok && liveStatus != "" {
			inst.Status = liveStatus
		}

		rule := rulesMap[inst.ID]
		if rule == nil {
			// 未入库时初始化默认规则（actions_enabled 默认 false）
			rule = &GuardRule{
				ID:              ulid.New(),
				CloudResourceID: inst.ID,
				IsEnabled:       true,
				ActionsEnabled:  false, // ★ 默认 false 安全约束
				TrafficAction:   "stop",
				ScheduleTZ:      e.cfg.DefaultTimezone,
			}
		}

		item := DecideInstance(inst, rule, cdtUsedGB, cdtErr, now)
		item.AccountName = acc.Name
		cycleEvaluated++

		// 80% 达阈值前预警 (P2-04 §4)
		if ShouldPrewarn(rule, cdtUsedGB) {
			events.Emit(ctx, events.Event{
				Type:       "cloud.guard.traffic_warning",
				Source:     "guard",
				TargetKind: "cloud_resource",
				TargetID:   inst.ID,
				Title:      fmt.Sprintf("CDT 流量预警: %s 已达限额 80%%", inst.Name),
				Payload: map[string]any{
					"instance_name":    inst.Name,
					"region":           inst.Region,
					"cdt_used_gb":      cdtUsedGB,
					"traffic_limit_gb": *rule.TrafficLimitGB,
					"usage_percent":    (cdtUsedGB / *rule.TrafficLimitGB) * 100.0,
					"account_name":     acc.Name,
				},
				DedupKey: fmt.Sprintf("guard:%s:cloud.guard.traffic_warning:%s", rule.ID, monthKey),
			})
		}

		// 动作执行 (或演练记录)
		if item.ProposedAction == ActionStop {
			if rule.ActionsEnabled && !dryRun {
				job, err := e.submitECSJob(ctx, inst, acc, "stop", item.Reason)
				if err != nil {
					cycleFailed++
					item.Error = err.Error()
					logx.Error("guard: submit stop job failed", "instance", inst.Name, "err", err)
				} else {
					cycleActed++
					item.JobSubmitted = true
					item.JobID = job.ID
					actionName := "traffic_stop"
					if item.Reason == "处于计划关机时段" {
						actionName = "schedule_stop"
					}
					_ = e.store.UpdateRuleLastAction(ctx, rule.ID, actionName, time.Now().UnixMilli(), time.Now().UnixMilli())
					_ = audit.Log(ctx, e.db, audit.Entry{
						ActorKind:  "system",
						Action:     "guard.instance_stopped",
						TargetKind: "cloud_resource",
						TargetID:   inst.ID,
						Detail:     fmt.Sprintf("stopped %s: %s", inst.Name, item.Reason),
						Result:     "submitted",
					})
					events.Emit(ctx, events.Event{
						Type:       "cloud.guard.instance_stopped",
						Source:     "guard",
						TargetKind: "cloud_resource",
						TargetID:   inst.ID,
						Title:      fmt.Sprintf("ECS 实例已自动关机: %s", inst.Name),
						Payload: map[string]any{
							"instance_name":    inst.Name,
							"region":           inst.Region,
							"cdt_used_gb":      cdtUsedGB,
							"traffic_limit_gb": getLimitVal(rule.TrafficLimitGB),
							"status_before":    inst.Status,
							"status_after":     "Stopping",
							"reason":           item.Reason,
						},
						DedupKey: fmt.Sprintf("guard:%s:cloud.guard.instance_stopped:%s", rule.ID, monthKey),
					})
				}
			} else {
				// actions_enabled=false 或 dryRun: 不动手
				actionName := "traffic_stop"
				if item.Reason == "处于计划关机时段" {
					actionName = "schedule_stop"
				}
				_ = e.store.UpdateRuleLastAction(ctx, rule.ID, actionName, time.Now().UnixMilli(), time.Now().UnixMilli())
				events.Emit(ctx, events.Event{
					Type:       "cloud.guard.instance_stopped",
					Source:     "guard",
					TargetKind: "cloud_resource",
					TargetID:   inst.ID,
					Title:      fmt.Sprintf("[模拟] ECS 实例触发关机条件: %s", inst.Name),
					Payload: map[string]any{
						"instance_name":    inst.Name,
						"region":           inst.Region,
						"cdt_used_gb":      cdtUsedGB,
						"traffic_limit_gb": getLimitVal(rule.TrafficLimitGB),
						"status_before":    inst.Status,
						"status_after":     inst.Status,
						"reason":           item.Reason + " (actions_enabled=false)",
					},
					DedupKey: fmt.Sprintf("guard:%s:cloud.guard.instance_stopped:%s", rule.ID, monthKey),
				})
			}
		} else if item.ProposedAction == ActionStart {
			if rule.ActionsEnabled && !dryRun {
				job, err := e.submitECSJob(ctx, inst, acc, "start", item.Reason)
				if err != nil {
					cycleFailed++
					item.Error = err.Error()
					logx.Error("guard: submit start job failed", "instance", inst.Name, "err", err)
				} else {
					cycleActed++
					item.JobSubmitted = true
					item.JobID = job.ID
					actionName := "schedule_start"
					if strings.Contains(item.Reason, "保活") {
						actionName = "keepalive_start"
					}
					_ = e.store.UpdateRuleLastAction(ctx, rule.ID, actionName, time.Now().UnixMilli(), time.Now().UnixMilli())
					_ = audit.Log(ctx, e.db, audit.Entry{
						ActorKind:  "system",
						Action:     "guard.instance_started",
						TargetKind: "cloud_resource",
						TargetID:   inst.ID,
						Detail:     fmt.Sprintf("started %s: %s", inst.Name, item.Reason),
						Result:     "submitted",
					})
					events.Emit(ctx, events.Event{
						Type:       "cloud.guard.instance_started",
						Source:     "guard",
						TargetKind: "cloud_resource",
						TargetID:   inst.ID,
						Title:      fmt.Sprintf("ECS 实例已自动开机: %s", inst.Name),
						Payload: map[string]any{
							"instance_name":    inst.Name,
							"region":           inst.Region,
							"cdt_used_gb":      cdtUsedGB,
							"traffic_limit_gb": getLimitVal(rule.TrafficLimitGB),
							"status_before":    inst.Status,
							"status_after":     "Starting",
							"reason":           item.Reason,
						},
						DedupKey: fmt.Sprintf("guard:%s:cloud.guard.instance_started:%s", rule.ID, monthKey),
					})
				}
			} else {
				actionName := "schedule_start"
				if strings.Contains(item.Reason, "保活") {
					actionName = "keepalive_start"
				}
				_ = e.store.UpdateRuleLastAction(ctx, rule.ID, actionName, time.Now().UnixMilli(), time.Now().UnixMilli())
				events.Emit(ctx, events.Event{
					Type:       "cloud.guard.instance_started",
					Source:     "guard",
					TargetKind: "cloud_resource",
					TargetID:   inst.ID,
					Title:      fmt.Sprintf("[模拟] ECS 实例触发开机条件: %s", inst.Name),
					Payload: map[string]any{
						"instance_name":    inst.Name,
						"region":           inst.Region,
						"cdt_used_gb":      cdtUsedGB,
						"traffic_limit_gb": getLimitVal(rule.TrafficLimitGB),
						"status_before":    inst.Status,
						"status_after":     inst.Status,
						"reason":           item.Reason + " (actions_enabled=false)",
					},
					DedupKey: fmt.Sprintf("guard:%s:cloud.guard.instance_started:%s", rule.ID, monthKey),
				})
			}
		} else {
			// Noop
			_ = e.store.UpdateRuleLastEval(ctx, rule.ID, time.Now().UnixMilli())
		}

		result.Items = append(result.Items, item)
	}

	result.EvaluatedCount += cycleEvaluated
	result.ActedCount += cycleActed
	result.FailedCount += cycleFailed

	// 保存 guard_cycles 记录
	if !dryRun {
		cycleRec := &GuardCycle{
			ID:             ulid.New(),
			CloudAccountID: acc.ID,
			StartedAtMs:    cycleStartMs,
			DurationMs:     int(time.Now().UnixMilli() - cycleStartMs),
			CDTUsedGB:      &cdtUsedGB,
			Evaluated:      cycleEvaluated,
			Acted:          cycleActed,
			Failed:         cycleFailed,
		}
		if cdtErr != nil {
			cycleRec.CDTError = cdtErr.Error()
		}
		_ = e.store.RecordCycle(ctx, cycleRec)
	}
}

func (e *Engine) submitECSJob(ctx context.Context, inst cloud.CloudResource, acc cloud.CloudAccount, action, reason string) (*jobs.Job, error) {
	if e.jobEngine == nil {
		return nil, errors.New("job engine is not initialized")
	}

	kind := JobKindECSStart
	if action == "stop" {
		kind = JobKindECSStop
	}

	req := jobs.SubmitRequest{
		Kind:         kind,
		TargetKind:   "cloud_resource",
		TargetID:     inst.ID,
		RejectIfBusy: true, // ★ 安全性质 4: 同一实例上一个 job 没结束前不提交新 job
		Params: map[string]any{
			"account_id":    acc.ID,
			"resource_id":   inst.ID,
			"ref":           inst.ResRef,
			"region":        inst.Region,
			"provider_code": acc.ProviderCode,
			"action":        action,
			"reason":        reason,
		},
		CreatedBy: "guard",
	}

	return e.jobEngine.Submit(ctx, req)
}

func (e *Engine) loadAccountInstances(ctx context.Context, accountID string) ([]cloud.CloudResource, error) {
	q := `SELECT id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, public_ips, private_ips, specs_json, billing_json, attrs_json, synced_at_ms, is_deleted, created_at_ms, updated_at_ms
FROM cloud_resources WHERE cloud_account_id = ? AND res_kind = 'ecs' AND is_deleted = 0`

	rows, err := e.db.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []cloud.CloudResource
	for rows.Next() {
		var r cloud.CloudResource
		var name, region, status, pubIPs, privIPs, specs, bill, attrs sql.NullString
		var isDel int
		if err := rows.Scan(
			&r.ID, &r.CloudAccountID, &r.ProviderCode, &r.ResKind, &r.ResRef,
			&name, &region, &status, &pubIPs, &privIPs,
			&specs, &bill, &attrs, &r.SyncedAtMs, &isDel,
			&r.CreatedAtMs, &r.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if name.Valid {
			r.Name = name.String
		}
		if region.Valid {
			r.Region = region.String
		}
		if status.Valid {
			r.Status = status.String
		}
		if pubIPs.Valid {
			r.PublicIPs = pubIPs.String
		}
		if privIPs.Valid {
			r.PrivateIPs = privIPs.String
		}
		if specs.Valid {
			r.SpecsJSON = specs.String
		}
		if bill.Valid {
			r.BillingJSON = bill.String
		}
		if attrs.Valid {
			r.AttrsJSON = attrs.String
		}
		r.IsDeleted = isDel == 1
		list = append(list, r)
	}
	return list, rows.Err()
}

func getLimitVal(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
