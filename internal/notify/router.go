package notify

import (
	"strings"
	"sync"
	"time"
)

// Router evaluates notification rules against events.
type Router struct {
	mu           sync.RWMutex
	lastSentTime map[string]int64 // key: dedup_key -> timestamp_ms
}

// NewRouter initializes a Router.
func NewRouter() *Router {
	return &Router{
		lastSentTime: make(map[string]int64),
	}
}

// SeverityRank converts severity string to integer priority.
func SeverityRank(sev string) int {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 1
	}
}

// MatchPattern checks if an event type matches a pattern (e.g. "*", "node.*", "node.offline").
func MatchPattern(pattern, eventType string) bool {
	pattern = strings.TrimSpace(pattern)
	eventType = strings.TrimSpace(eventType)

	if pattern == "*" || pattern == eventType {
		return true
	}

	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}

	return false
}

// InQuietHours checks whether the current UTC time falls within a quiet period.
// quietStart and quietEnd are minutes from 0 to 1439 (00:00 to 23:59).
func InQuietHours(now time.Time, quietStart, quietEnd *int) bool {
	if quietStart == nil || quietEnd == nil {
		return false
	}
	start := *quietStart
	end := *quietEnd

	curMin := now.UTC().Hour()*60 + now.UTC().Minute()

	if start <= end {
		return curMin >= start && curMin <= end
	}
	// Spans across midnight, e.g. 23:00 (1380) to 07:00 (420)
	return curMin >= start || curMin <= end
}

// MatchResult represents an outcome of evaluating a rule.
type MatchResult struct {
	Rule         *Rule
	ChannelID    string
	TemplateName string
	IsThrottled  bool
	IsQuietHeld  bool
}

// EvaluateRules matches an event against all active rules and deduplicates by channel.
func (r *Router) EvaluateRules(rules []*Rule, eventType, severity, dedupKey, targetID string, now time.Time) []MatchResult {
	nowMs := now.UnixMilli()
	eventRank := SeverityRank(severity)

	var matched []MatchResult
	// Channel deduplication map: channel_id -> index in matched
	channelSeen := make(map[string]int)

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rule := range rules {
		if !rule.IsEnabled {
			continue
		}

		// 1. Check pattern match
		if !MatchPattern(rule.EventPattern, eventType) {
			continue
		}

		// 2. Check min severity
		if eventRank < SeverityRank(rule.MinSeverity) {
			continue
		}

		channelID := rule.NotifyChannelID
		if channelID == "" {
			continue
		}

		// 3. Check quiet hours
		quietHeld := InQuietHours(now, rule.QuietStartMin, rule.QuietEndMin)

		// 4. Check throttle / dedup
		throttled := false
		if rule.ThrottleS > 0 {
			key := dedupKey
			if key == "" {
				if targetID != "" {
					key = rule.ID + ":" + targetID
				} else {
					key = rule.ID + ":" + eventType
				}
			}

			if lastMs, exists := r.lastSentTime[key]; exists {
				if nowMs-lastMs < int64(rule.ThrottleS)*1000 {
					throttled = true
				}
			}

			if !throttled && !quietHeld {
				r.lastSentTime[key] = nowMs
			}
		}

		res := MatchResult{
			Rule:         rule,
			ChannelID:    channelID,
			TemplateName: rule.TemplateName,
			IsThrottled:  throttled,
			IsQuietHeld:  quietHeld,
		}

		if idx, seen := channelSeen[channelID]; seen {
			// If already routed to this channel, prefer non-throttled / non-quiet state
			if !res.IsThrottled && !res.IsQuietHeld {
				matched[idx] = res
			}
		} else {
			channelSeen[channelID] = len(matched)
			matched = append(matched, res)
		}
	}

	return matched
}

// ResetThrottleCache clears in-memory throttle cache.
func (r *Router) ResetThrottleCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSentTime = make(map[string]int64)
}
