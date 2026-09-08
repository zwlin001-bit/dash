package guard

import (
	"fmt"
	"strings"
	"time"

	"dash/internal/cloud"
)

// EffectiveRule 计算实例在账号策略与设备规则合并后的最终生效参数，并标记来源。
type EffectiveRule struct {
	IsEnabled       bool
	ActionsEnabled  bool // 双重门锁：两级都为 true 才动手
	TrafficLimitGB  *float64
	TrafficAction   string
	WarnRatio       float64
	ScheduleEnabled bool
	ScheduleStart   *string
	ScheduleStop    *string
	ScheduleTZ      string
	LimitOrigin     string // "account" | "resource"
	ScheduleOrigin  string // "account" | "resource"
	LastAction      *string
}

// ResolveEffectiveRule 按照 P2-11 §1 逐字段覆盖计算生效规则。
func ResolveEffectiveRule(rule *GuardRule, acctPolicy *GuardAccountPolicy) EffectiveRule {
	var eff EffectiveRule
	eff.WarnRatio = 0.8
	eff.TrafficAction = "stop"
	eff.ScheduleTZ = "Asia/Shanghai"
	eff.LimitOrigin = "account"
	eff.ScheduleOrigin = "account"

	// 1. 账号级默认
	if acctPolicy != nil {
		eff.IsEnabled = acctPolicy.IsEnabled
		eff.ActionsEnabled = acctPolicy.ActionsEnabled
		eff.TrafficLimitGB = acctPolicy.TrafficLimitGB
		if acctPolicy.TrafficAction != "" {
			eff.TrafficAction = acctPolicy.TrafficAction
		}
		if acctPolicy.WarnRatio > 0 {
			eff.WarnRatio = acctPolicy.WarnRatio
		}
		eff.ScheduleEnabled = acctPolicy.ScheduleEnabled
		eff.ScheduleStart = acctPolicy.ScheduleStart
		eff.ScheduleStop = acctPolicy.ScheduleStop
		if acctPolicy.ScheduleTZ != "" {
			eff.ScheduleTZ = acctPolicy.ScheduleTZ
		}
	} else {
		// 没有账号策略时，默认账号级 is_enabled=true, actions_enabled=false
		eff.IsEnabled = true
		eff.ActionsEnabled = false
	}

	if rule == nil {
		return eff
	}

	eff.LastAction = rule.LastAction

	// 2. 设备级覆盖
	if !rule.InheritAccount {
		// 设备自定义
		eff.IsEnabled = rule.IsEnabled
		// ★ actions_enabled: 两级都为 true 才动手 (P2-11 §1)
		eff.ActionsEnabled = eff.ActionsEnabled && rule.ActionsEnabled

		if rule.TrafficLimitGB != nil {
			eff.TrafficLimitGB = rule.TrafficLimitGB
			eff.LimitOrigin = "resource"
		}
		if rule.TrafficAction != "" {
			eff.TrafficAction = rule.TrafficAction
		}
		if rule.ScheduleEnabled {
			eff.ScheduleEnabled = true
			eff.ScheduleStart = rule.ScheduleStart
			eff.ScheduleStop = rule.ScheduleStop
			if rule.ScheduleTZ != "" {
				eff.ScheduleTZ = rule.ScheduleTZ
			}
			eff.ScheduleOrigin = "resource"
		}
	} else {
		// 设备跟随账号 (inherit_account = 1)
		eff.IsEnabled = rule.IsEnabled
		// ★ actions_enabled: 两级都为 true 才动手 (P2-11 §1)
		eff.ActionsEnabled = eff.ActionsEnabled && rule.ActionsEnabled

		// traffic_limit_gb: 设备若显式配置了值则设备覆盖，否则用账号的
		if rule.TrafficLimitGB != nil {
			eff.TrafficLimitGB = rule.TrafficLimitGB
			eff.LimitOrigin = "resource"
		} else {
			eff.LimitOrigin = "account"
		}

		// schedule: 设备若启用了日程则使用设备的，否则使用账号的
		if rule.ScheduleEnabled {
			eff.ScheduleEnabled = true
			eff.ScheduleStart = rule.ScheduleStart
			eff.ScheduleStop = rule.ScheduleStop
			if rule.ScheduleTZ != "" {
				eff.ScheduleTZ = rule.ScheduleTZ
			}
			eff.ScheduleOrigin = "resource"
		} else {
			eff.ScheduleOrigin = "account"
		}
	}

	return eff
}

// DecideInstance 根据规格执行逐实例决策 (P2-04 §2 / P2-11 §1)。
// 决策优先级: 日程 > 流量 > 保活，逐条判断，命中即停。
func DecideInstance(res cloud.CloudResource, rule *GuardRule, acctPolicy *GuardAccountPolicy, cdtUsedGB float64, cdtErr error, now time.Time) EvaluationItem {
	item := EvaluationItem{
		ResourceID:     res.ID,
		ResourceName:   res.Name,
		ResourceRef:    res.ResRef,
		Region:         res.Region,
		AccountID:      res.CloudAccountID,
		CurrentStatus:  res.Status,
		ProposedAction: ActionNoop,
		CDTUsedGB:      cdtUsedGB,
	}

	if res.Name == "" {
		item.ResourceName = res.ResRef
	}

	eff := ResolveEffectiveRule(rule, acctPolicy)

	// 账号级总开关 is_enabled=false 或 设备级 is_enabled=false
	if acctPolicy != nil && !acctPolicy.IsEnabled {
		item.Reason = "账号级守卫已停用"
		item.DecidedBy = "account"
		return item
	}

	if rule != nil && !rule.IsEnabled {
		item.Reason = "实例守卫规则已停用"
		item.DecidedBy = "resource"
		return item
	}

	item.ActionsEnabled = eff.ActionsEnabled
	item.TrafficLimitGB = eff.TrafficLimitGB
	if eff.TrafficLimitGB != nil && *eff.TrafficLimitGB > 0 {
		item.UsagePercent = (cdtUsedGB / *eff.TrafficLimitGB) * 100.0
	}

	// 安全性质 3: 过渡状态不动手 (Starting / Stopping / Pending 等)
	statusLower := strings.ToLower(strings.TrimSpace(res.Status))
	if statusLower == "starting" || statusLower == "stopping" || statusLower == "pending" {
		item.Reason = fmt.Sprintf("实例处于过渡状态 %q，跳过本轮", res.Status)
		item.ProposedAction = ActionNoop
		return item
	}

	// 统一标准状态: Running / Stopped
	isRunning := statusLower == "running"
	isStopped := statusLower == "stopped"

	// 评估日程
	var inScheduleStop, inScheduleRun bool
	if eff.ScheduleEnabled && eff.ScheduleStart != nil && eff.ScheduleStop != nil {
		inStop, inRun, nextAction, nextTime, err := EvaluateSchedule(*eff.ScheduleStart, *eff.ScheduleStop, eff.ScheduleTZ, now)
		if err == nil {
			inScheduleStop = inStop
			inScheduleRun = inRun
			item.InScheduleStop = inStop
			item.InScheduleRun = inRun
			item.NextScheduleAction = nextAction
			if nextTime != nil {
				ms := nextTime.UnixMilli()
				item.NextScheduleTimeMs = &ms
			}
		}
	}

	// 1. 条件 1: 日程启用 且 当前处于计划关机时段 且 实例 Running → 停止
	// 安全性质 1: 计划关机只依赖「ECS 状态可读」，CDT/BSS 查询失败不影响关机
	if eff.ScheduleEnabled && inScheduleStop && isRunning {
		item.ProposedAction = ActionStop
		item.Reason = "处于计划关机时段"
		item.WouldExecute = eff.ActionsEnabled
		item.DecidedBy = eff.ScheduleOrigin
		return item
	}

	// 2. 条件 2: 日程启用 且 当前处于计划运行时段 且 实例 Stopped 且 流量未超 → 启动
	if eff.ScheduleEnabled && inScheduleRun && isStopped {
		// 安全性质 2: 流量读不到时不许启动实例 (默认按不安全处理)
		if cdtErr != nil {
			item.ProposedAction = ActionNoop
			item.Reason = "处于计划运行时段，但CDT流量无法读取，按不安全策略本轮不启动"
			item.DecidedBy = eff.ScheduleOrigin
			return item
		}
		// 检查流量是否超限
		if eff.TrafficLimitGB != nil && cdtUsedGB >= *eff.TrafficLimitGB {
			item.ProposedAction = ActionNoop
			item.Reason = fmt.Sprintf("处于计划运行时段，但CDT流量已达 %.2f GB (阈值: %.2f GB)，不予启动", cdtUsedGB, *eff.TrafficLimitGB)
			item.DecidedBy = eff.LimitOrigin
			return item
		}

		item.ProposedAction = ActionStart
		item.Reason = "处于计划运行时段且流量未超"
		item.WouldExecute = eff.ActionsEnabled
		item.DecidedBy = eff.ScheduleOrigin
		return item
	}

	// 3. 条件 3: 流量 ≥ 阈值 且 实例 Running → 停止
	if eff.TrafficLimitGB != nil && cdtUsedGB >= *eff.TrafficLimitGB && isRunning {
		item.ProposedAction = ActionStop
		item.Reason = fmt.Sprintf("CDT用量 %.2f GB 达到或超过限额 %.2f GB", cdtUsedGB, *eff.TrafficLimitGB)
		item.WouldExecute = eff.ActionsEnabled
		item.DecidedBy = eff.LimitOrigin
		return item
	}

	// 4. 条件 4: 流量 < 阈值 且 实例 Stopped 且 不在计划关机时段 → 启动（保活）
	if isStopped && (!eff.ScheduleEnabled || !inScheduleStop) {
		// 安全性质 2: 流量读不到时不许启动实例
		if cdtErr != nil {
			item.ProposedAction = ActionNoop
			item.Reason = "实例处于已停止状态，但CDT流量无法读取，按不安全策略本轮不启动"
			item.DecidedBy = eff.LimitOrigin
			return item
		}

		// 检查流量超限
		trafficExceeded := eff.TrafficLimitGB != nil && cdtUsedGB >= *eff.TrafficLimitGB
		if trafficExceeded {
			item.ProposedAction = ActionNoop
			item.Reason = fmt.Sprintf("实例处于停止状态，CDT用量 %.2f GB 已超限额 %.2f GB", cdtUsedGB, *eff.TrafficLimitGB)
			item.DecidedBy = eff.LimitOrigin
			return item
		}

		// 若实例是因为 schedule_stop 被停下，在 schedule_enabled 关掉后保持原状
		if eff.LastAction != nil && *eff.LastAction == "schedule_stop" && !eff.ScheduleEnabled {
			item.ProposedAction = ActionNoop
			item.Reason = "此前由计划日程停机，日程停用后保持当前停止状态"
			item.DecidedBy = eff.ScheduleOrigin
			return item
		}

		// 若日程开启中但不在运行期
		if eff.ScheduleEnabled && !inScheduleRun {
			item.ProposedAction = ActionNoop
			item.Reason = "当前未处于计划运行时段"
			item.DecidedBy = eff.ScheduleOrigin
			return item
		}

		item.ProposedAction = ActionStart
		item.Reason = "保活拉起 (流量正常且不在计划关机时段)"
		item.WouldExecute = eff.ActionsEnabled
		item.DecidedBy = eff.LimitOrigin
		return item
	}

	// 5. 其他 → 不动
	item.ProposedAction = ActionNoop
	item.Reason = "状态正常，无需变更"
	return item
}

// ShouldPrewarn 检查是否满足预警条件 (P2-04 §4 / P2-11 §1)。
func ShouldPrewarn(rule *GuardRule, acctPolicy *GuardAccountPolicy, cdtUsedGB float64) bool {
	eff := ResolveEffectiveRule(rule, acctPolicy)
	if !eff.IsEnabled || eff.TrafficLimitGB == nil || *eff.TrafficLimitGB <= 0 {
		return false
	}
	limit := *eff.TrafficLimitGB
	ratio := cdtUsedGB / limit
	warnRatio := eff.WarnRatio
	if warnRatio <= 0 {
		warnRatio = 0.8
	}
	return ratio >= warnRatio && ratio < 1.0
}
