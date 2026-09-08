package guard

import (
	"fmt"
	"strings"
	"time"

	"dash/internal/cloud"
)

// DecideInstance 根据规格执行逐实例决策 (P2-04 §2)。
// 决策优先级: 日程 > 流量 > 保活，逐条判断，命中即停。
func DecideInstance(res cloud.CloudResource, rule *GuardRule, cdtUsedGB float64, cdtErr error, now time.Time) EvaluationItem {
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

	if rule == nil || !rule.IsEnabled {
		item.Reason = "未配置规则或规则已停用"
		return item
	}

	item.ActionsEnabled = rule.ActionsEnabled
	item.TrafficLimitGB = rule.TrafficLimitGB
	if rule.TrafficLimitGB != nil && *rule.TrafficLimitGB > 0 {
		item.UsagePercent = (cdtUsedGB / *rule.TrafficLimitGB) * 100.0
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
	if rule.ScheduleEnabled && rule.ScheduleStart != nil && rule.ScheduleStop != nil {
		inStop, inRun, nextAction, nextTime, err := EvaluateSchedule(*rule.ScheduleStart, *rule.ScheduleStop, rule.ScheduleTZ, now)
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
	if rule.ScheduleEnabled && inScheduleStop && isRunning {
		item.ProposedAction = ActionStop
		item.Reason = "处于计划关机时段"
		item.WouldExecute = rule.ActionsEnabled
		return item
	}

	// 2. 条件 2: 日程启用 且 当前处于计划运行时段 且 实例 Stopped 且 流量未超 → 启动
	if rule.ScheduleEnabled && inScheduleRun && isStopped {
		// 安全性质 2: 流量读不到时不许启动实例 (默认按不安全处理)
		if cdtErr != nil {
			item.ProposedAction = ActionNoop
			item.Reason = "处于计划运行时段，但CDT流量无法读取，按不安全策略本轮不启动"
			return item
		}
		// 检查流量是否超限
		if rule.TrafficLimitGB != nil && cdtUsedGB >= *rule.TrafficLimitGB {
			item.ProposedAction = ActionNoop
			item.Reason = fmt.Sprintf("处于计划运行时段，但CDT流量已达 %.2f GB (阈值: %.2f GB)，不予启动", cdtUsedGB, *rule.TrafficLimitGB)
			return item
		}

		item.ProposedAction = ActionStart
		item.Reason = "处于计划运行时段且流量未超"
		item.WouldExecute = rule.ActionsEnabled
		return item
	}

	// 3. 条件 3: 流量 ≥ 阈值 且 实例 Running → 停止
	if rule.TrafficLimitGB != nil && cdtUsedGB >= *rule.TrafficLimitGB && isRunning {
		item.ProposedAction = ActionStop
		item.Reason = fmt.Sprintf("CDT用量 %.2f GB 达到或超过限额 %.2f GB", cdtUsedGB, *rule.TrafficLimitGB)
		item.WouldExecute = rule.ActionsEnabled
		return item
	}

	// 4. 条件 4: 流量 < 阈值 且 实例 Stopped 且 不在计划关机时段 → 启动（保活）
	// 注意: 如果日程未启用，不在计划关机时段自然成立
	if isStopped && (!rule.ScheduleEnabled || !inScheduleStop) {
		// 安全性质 2: 流量读不到时不许启动实例
		if cdtErr != nil {
			item.ProposedAction = ActionNoop
			item.Reason = "实例处于已停止状态，但CDT流量无法读取，按不安全策略本轮不启动"
			return item
		}

		// 检查流量超限
		trafficExceeded := rule.TrafficLimitGB != nil && cdtUsedGB >= *rule.TrafficLimitGB
		if trafficExceeded {
			item.ProposedAction = ActionNoop
			item.Reason = fmt.Sprintf("实例处于停止状态，CDT用量 %.2f GB 已超限额 %.2f GB", cdtUsedGB, *rule.TrafficLimitGB)
			return item
		}

		// 验收 5 关键语义:
		// "关掉日程开关 → 不会立刻改变当前状态，只是不再受时间约束（这是刻意的行为）"
		// 若实例是因为 schedule_stop 被停下，在 schedule_enabled 关掉后不应立刻盲目启动它
		if rule.LastAction != nil && *rule.LastAction == "schedule_stop" && !rule.ScheduleEnabled {
			item.ProposedAction = ActionNoop
			item.Reason = "此前由计划日程停机，日程停用后保持当前停止状态"
			return item
		}

		// 若日程开启中但不在运行期(例如无运行期定义或处于过渡时点)
		if rule.ScheduleEnabled && !inScheduleRun {
			item.ProposedAction = ActionNoop
			item.Reason = "当前未处于计划运行时段"
			return item
		}

		item.ProposedAction = ActionStart
		item.Reason = "保活拉起 (流量正常且不在计划关机时段)"
		item.WouldExecute = rule.ActionsEnabled
		return item
	}

	// 5. 其他 → 不动
	item.ProposedAction = ActionNoop
	item.Reason = "状态正常，无需变更"
	return item
}

// ShouldPrewarn 检查是否满足 80% 预警条件 (P2-04 §4)。
func ShouldPrewarn(rule *GuardRule, cdtUsedGB float64) bool {
	if rule == nil || !rule.IsEnabled || rule.TrafficLimitGB == nil || *rule.TrafficLimitGB <= 0 {
		return false
	}
	limit := *rule.TrafficLimitGB
	ratio := cdtUsedGB / limit
	return ratio >= 0.8 && ratio < 1.0
}
