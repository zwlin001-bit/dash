package guard

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTimeOfDay 将 "HH:MM" 字符串解析为从当天零点起算的小时和分钟。
func ParseTimeOfDay(s string) (hour int, min int, err error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format %q, expected HH:MM", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour %q: %w", parts[0], err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute %q: %w", parts[1], err)
	}
	return h, m, nil
}

// LoadLocationWithFallback 加载 IANA 时区，失败时回退至 Asia/Shanghai 或 Local。
func LoadLocationWithFallback(tzName string) *time.Location {
	tzName = strings.TrimSpace(tzName)
	if tzName == "" {
		tzName = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tzName)
	if err == nil {
		return loc
	}
	// Fallback to UTC+8 fixed zone if Asia/Shanghai tzdata is missing
	if tzName == "Asia/Shanghai" || tzName == "PRC" {
		return time.FixedZone("CST", 8*3600)
	}
	return time.Local
}

// EvaluateSchedule 判断给定的当前时间在指定日程配置下的状态。
// 返回:
// inStop: 当前是否处于计划关机时段
// inRun: 当前是否处于计划运行时段
// nextAction: 下一个计划动作 ("start" 或 "stop"，空字符串表示无日程)
// nextTime: 下一个计划动作触发的时间 (UTC 时间)
func EvaluateSchedule(startStr, stopStr, tzStr string, now time.Time) (inStop bool, inRun bool, nextAction string, nextTime *time.Time, err error) {
	if startStr == "" || stopStr == "" {
		return false, false, "", nil, errors.New("schedule_start and schedule_stop cannot be empty")
	}

	loc := LoadLocationWithFallback(tzStr)
	nowInLoc := now.In(loc)

	startHour, startMin, err := ParseTimeOfDay(startStr)
	if err != nil {
		return false, false, "", nil, err
	}
	stopHour, stopMin, err := ParseTimeOfDay(stopStr)
	if err != nil {
		return false, false, "", nil, err
	}

	startMinutes := startHour*60 + startMin
	stopMinutes := stopHour*60 + stopMin
	nowMinutes := nowInLoc.Hour()*60 + nowInLoc.Minute()

	// 构造当天的 start 和 stop 时间点
	todayStart := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), startHour, startMin, 0, 0, loc)
	todayStop := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), stopHour, stopMin, 0, 0, loc)

	if startMinutes < stopMinutes {
		// 日间开机模式: 例如 08:30 开机, 20:00 关机
		// [08:30, 20:00) 为运行时段，其余为关机时段
		if nowMinutes >= startMinutes && nowMinutes < stopMinutes {
			inRun = true
			inStop = false
			nextAction = "stop"
			t := todayStop.UTC()
			nextTime = &t
		} else {
			inRun = false
			inStop = true
			nextAction = "start"
			var t time.Time
			if nowMinutes < startMinutes {
				t = todayStart.UTC()
			} else {
				// 已经过了今天的开机时间，下一次开机是明天的 start
				t = todayStart.AddDate(0, 0, 1).UTC()
			}
			nextTime = &t
		}
	} else if startMinutes > stopMinutes {
		// 跨夜开机模式: 例如 22:00 开机, 次日 06:00 关机
		// [22:00, 24:00) U [00:00, 06:00) 为运行时段，其余为关机时段
		if nowMinutes >= startMinutes || nowMinutes < stopMinutes {
			inRun = true
			inStop = false
			nextAction = "stop"
			var t time.Time
			if nowMinutes >= startMinutes {
				// 下一次关机是明天的 stop
				t = todayStop.AddDate(0, 0, 1).UTC()
			} else {
				// 当前在凌晨，下一次关机是今天的 stop
				t = todayStop.UTC()
			}
			nextTime = &t
		} else {
			inRun = false
			inStop = true
			nextAction = "start"
			t := todayStart.UTC()
			nextTime = &t
		}
	} else {
		// start == stop 无效
		return false, false, "", nil, errors.New("schedule_start and schedule_stop cannot be equal")
	}

	return inStop, inRun, nextAction, nextTime, nil
}
