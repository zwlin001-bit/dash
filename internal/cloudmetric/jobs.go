package cloudmetric

import (
	"time"

	"dash/internal/jobs"
)

const (
	JobKindCloudMetricSync = "cloud.metric.sync"
)

// RegisterJobs registers cloud.metric.sync in the Job engine.
func RegisterJobs(reg *jobs.Registry, syncer *Syncer) {
	if reg == nil || syncer == nil {
		return
	}

	reg.Register(jobs.JobDefinition{
		Kind:        JobKindCloudMetricSync,
		Description: "云侧监控指标与账号 CDT 流量增量拉取 (P2-10)",
		Timeout:     5 * time.Minute,
		MaxAttempt:  2,
		Steps: []jobs.StepDef{
			{
				Name:       "执行云侧指标增量同步",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("开始执行云侧监控指标与 CDT 流量增量拉取...")
					report, err := syncer.SyncOnce(sctx)
					if err != nil {
						sctx.Log("同步失败: %v", err)
						return err
					}
					sctx.Log("同步完成: 账号=%d, 实例=%d, 新增点数=%d, 异常数=%d, 耗时=%dms",
						report.SyncedAccounts, report.SyncedInstances, report.PointsSaved, report.ErrorsCount, report.DurationMs)
					sctx.SetResult(map[string]any{
						"synced_accounts":  report.SyncedAccounts,
						"synced_instances": report.SyncedInstances,
						"points_saved":     report.PointsSaved,
						"errors_count":     report.ErrorsCount,
						"duration_ms":      report.DurationMs,
					})
					return nil
				},
			},
		},
	})
}
