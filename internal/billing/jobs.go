package billing

import (
	"context"
	"fmt"
	"time"

	"dash/internal/alert"
	"dash/internal/jobs"
)

const (
	JobKindBillSync = "bill.sync"
)

// RegisterBillingJobs registers bill.sync into Job registry.
func RegisterBillingJobs(reg *jobs.Registry, svc *Service, alertEng *alert.Engine) {
	if reg == nil || svc == nil {
		return
	}

	reg.Register(jobs.JobDefinition{
		Kind:        JobKindBillSync,
		Description: "同步云厂商月度账单明细、补齐历史数据并进行预算评估",
		Timeout:     10 * time.Minute,
		MaxAttempt:  2,
		Steps: []jobs.StepDef{
			{
				Name:       "同步各云账号账单与明细条目",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("开始拉取云账号月度总览与资源明细...")
					ctx := context.Background()
					if err := svc.SyncAll(ctx, 12); err != nil {
						sctx.Log(fmt.Sprintf("账单同步部分或全部失败: %v", err))
						return err
					}
					sctx.Log("账单明细同步与未关联资源关联归档完成。")
					return nil
				},
			},
			{
				Name:       "预算评估与超限告警触发",
				Idempotent: true,
				Run: func(sctx *jobs.StepContext) error {
					sctx.Log("开始执行月度预算使用率核算...")
					if alertEng != nil {
						ctx := context.Background()
						if err := alertEng.EvaluateKind(ctx, alert.RuleKindBudget); err != nil {
							sctx.Log(fmt.Sprintf("预算评估告警引擎执行异常: %v", err))
							return err
						}
					}
					sctx.Log("预算核算与预警评估完毕。")
					return nil
				},
			},
		},
	})
}
