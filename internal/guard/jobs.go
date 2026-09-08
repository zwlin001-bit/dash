package guard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/jobs"
	"dash/internal/logx"
	"dash/internal/provider"
)

const (
	JobKindECSStart = "ecs.start_instance"
	JobKindECSStop  = "ecs.stop_instance"
)

// RegisterGuardJobs 向 Job 引擎注册 ECS 启停任务定义。
func RegisterGuardJobs(reg *jobs.Registry, d *db.DB, credStore *credentials.Store, pm *provider.Manager) {
	if reg == nil {
		return
	}

	// 1. 注册 ecs.start_instance 任务
	reg.Register(jobs.JobDefinition{
		Kind:        JobKindECSStart,
		Description: "启动 ECS 实例并等待确认 Running 状态 (90s 预算)",
		Timeout:     3 * time.Minute,
		MaxAttempt:  2,
		Steps: []jobs.StepDef{
			{
				Name:       "调用云 API 发起开机",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("正在准备调用云 API 启动实例...")
					return executeInstanceAction(sctx, d, credStore, pm, "start")
				},
			},
			{
				Name:       "轮询确认 Running 状态",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("开始轮询实例状态，预算 90 秒，每 5 秒一轮...")
					return waitForInstanceStatus(sctx, d, credStore, pm, "Running", 90*time.Second, 5*time.Second)
				},
			},
		},
	})

	// 2. 注册 ecs.stop_instance 任务
	reg.Register(jobs.JobDefinition{
		Kind:        JobKindECSStop,
		Description: "停止 ECS 实例并等待确认 Stopped 状态 (45s 预算)",
		Timeout:     3 * time.Minute,
		MaxAttempt:  2,
		Steps: []jobs.StepDef{
			{
				Name:       "调用云 API 发起关机",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("正在准备调用云 API 停止实例...")
					return executeInstanceAction(sctx, d, credStore, pm, "stop")
				},
			},
			{
				Name:       "轮询确认 Stopped 状态",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("开始轮询实例状态，预算 45 秒，每 5 秒一轮...")
					return waitForInstanceStatus(sctx, d, credStore, pm, "Stopped", 45*time.Second, 5*time.Second)
				},
			},
		},
	})
}

func executeInstanceAction(sctx *jobs.StepContext, d *db.DB, credStore *credentials.Store, pm *provider.Manager, action string) error {
	accountID, _ := sctx.Params["account_id"].(string)
	ref, _ := sctx.Params["ref"].(string)
	region, _ := sctx.Params["region"].(string)
	providerCode, _ := sctx.Params["provider_code"].(string)
	if providerCode == "" {
		providerCode = "aliyun"
	}

	if ref == "" || region == "" {
		return fmt.Errorf("missing ref (%q) or region (%q) in job params", ref, region)
	}

	// 查取账号与凭据
	var credID string
	err := d.QueryRow(sctx, `SELECT credential_id FROM cloud_accounts WHERE id = ?`, accountID).Scan(&credID)
	if err != nil {
		return fmt.Errorf("lookup cloud account %s: %w", accountID, err)
	}

	_, pt, err := credStore.GetDecrypted(sctx, credID)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}

	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		return fmt.Errorf("parse credential payload: %w", err)
	}

	client, err := pm.GetClient(sctx, providerCode)
	if err != nil {
		return fmt.Errorf("get provider client %s: %w", providerCode, err)
	}

	sctx.Log("向 %s 发送 %s 指令 (实例: %s, 地域: %s)", providerCode, action, ref, region)
	resp, err := client.Action(sctx, provider.ActionParams{
		Credential: credMap,
		Region:     region,
		Kind:       "ecs",
		Ref:        ref,
		Action:     action,
	})
	if err != nil {
		sctx.Log("云 API 调用失败: %s", logx.Redact(err.Error()))
		return fmt.Errorf("provider action %s: %w", action, err)
	}

	sctx.Log("云 API 响应成功: %s (Handle: %s)", resp.Message, resp.JobHandle)
	sctx.SetSharedData("action_job_handle", resp.JobHandle)
	return nil
}

func waitForInstanceStatus(sctx *jobs.StepContext, d *db.DB, credStore *credentials.Store, pm *provider.Manager, targetStatus string, budget, interval time.Duration) error {
	accountID, _ := sctx.Params["account_id"].(string)
	ref, _ := sctx.Params["ref"].(string)
	region, _ := sctx.Params["region"].(string)
	providerCode, _ := sctx.Params["provider_code"].(string)
	resourceID, _ := sctx.Params["resource_id"].(string)
	if providerCode == "" {
		providerCode = "aliyun"
	}

	var credID, accountSite string
	_ = d.QueryRow(sctx, `SELECT credential_id, account_site FROM cloud_accounts WHERE id = ?`, accountID).Scan(&credID, &accountSite)

	_, pt, err := credStore.GetDecrypted(sctx, credID)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}
	var credMap map[string]string
	_ = json.Unmarshal(pt, &credMap)

	client, err := pm.GetClient(sctx, providerCode)
	if err != nil {
		return fmt.Errorf("get provider client: %w", err)
	}

	deadline := time.Now().Add(budget)
	round := 0
	lastStatus := ""

	for {
		round++
		select {
		case <-sctx.Done():
			return sctx.Err()
		default:
		}

		// 查取云端最新资源状态
		resources, err := client.ListResources(sctx, credMap, region, "ecs", accountSite)
		if err == nil {
			for _, r := range resources {
				if r.Ref == ref {
					lastStatus = r.Status
					break
				}
			}
		}

		sctx.Log("轮询第 %d 轮: 目标=%s, 当前=%s", round, targetStatus, lastStatus)

		if strings.EqualFold(lastStatus, targetStatus) {
			sctx.Log("✓ 确认到位: 实例 %s 已进入 %s 状态", ref, targetStatus)
			now := time.Now().UnixMilli()
			if resourceID != "" {
				_, _ = d.Exec(sctx, `UPDATE cloud_resources SET status = ?, synced_at_ms = ?, updated_at_ms = ? WHERE id = ?`, targetStatus, now, now, resourceID)
			}
			sctx.SetResult(map[string]any{
				"status":    targetStatus,
				"confirmed": true,
				"rounds":    round,
			})
			return nil
		}

		if time.Now().After(deadline) {
			// 超预算未到位 → 标记 warning（不当作失败中断，事件写明已提交但未确认）
			sctx.Log("⚠️ 超过预算时间 (%v) 仍未确认到位，当前状态为 %s (已提交但未确认)", budget, lastStatus)
			sctx.SetResult(map[string]any{
				"status":    lastStatus,
				"confirmed": false,
				"warning":   "已提交但未确认",
				"rounds":    round,
			})
			return nil
		}

		time.Sleep(interval)
	}
}
