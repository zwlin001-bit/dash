package events

import (
	"sync"
)

// Event 定义跨模块发布的标准事件结构 (P1-20 / 09-events-notify.md §2)
type Event struct {
	Type       string         `json:"event_type"`        // 如 "node.offline"
	Source     string         `json:"source_module"`     // 模块名，如 "control"
	TargetKind string         `json:"target_kind,omitempty"` // "node" / "user" / ""
	TargetID   string         `json:"target_id,omitempty"`
	Title      string         `json:"title"`
	Payload    map[string]any `json:"payload,omitempty"` // 模板变量，见 09-events-notify.md §5.2
	DedupKey   string         `json:"dedup_key,omitempty"` // 如 "node.offline:<node_id>"，可空
	OccurredAt int64          `json:"occurred_at_ms,omitempty"` // 0 表示用当前时间
}

// TypeDef 定义事件类型的元数据与处置策略 (10-schema-spec.md §4)
type TypeDef struct {
	Type               string `json:"event_type"`
	DisplayName        string `json:"display_name"`
	DefaultSeverity    string `json:"default_severity"`    // info / warning / critical
	Severity           string `json:"severity"`            // 当前生效级别，初始 = DefaultSeverity
	DefaultDisposition string `json:"default_disposition"` // drop / store / store+ui / store+notify
	Disposition        string `json:"disposition"`         // 当前处置方式，初始 = DefaultDisposition
	Description        string `json:"description"`
	IsBuiltin          bool   `json:"is_builtin"`
	UpdatedAtMs        int64  `json:"updated_at_ms"`
}

// TypePolicy 表示运行时轻量级缓存的判定策略
type TypePolicy struct {
	Severity    string
	Disposition string
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]TypeDef)
	policyMu   sync.RWMutex
	policies   = make(map[string]TypePolicy)
)

// RegisterType 注册内置事件类型（进程启动或包 init 时调用）。
func RegisterType(t TypeDef) {
	if t.Type == "" {
		return
	}
	if t.DefaultSeverity == "" {
		t.DefaultSeverity = "info"
	}
	if t.Severity == "" {
		t.Severity = t.DefaultSeverity
	}
	if t.DefaultDisposition == "" {
		t.DefaultDisposition = "store"
	}
	if t.Disposition == "" {
		t.Disposition = t.DefaultDisposition
	}
	t.IsBuiltin = true

	registryMu.Lock()
	registry[t.Type] = t
	registryMu.Unlock()

	policyMu.Lock()
	policies[t.Type] = TypePolicy{
		Severity:    t.Severity,
		Disposition: t.Disposition,
	}
	policyMu.Unlock()
}

// GetRegisteredTypes 获取所有已注册的内置类型定义。
func GetRegisteredTypes() []TypeDef {
	registryMu.RLock()
	defer registryMu.RUnlock()
	res := make([]TypeDef, 0, len(registry))
	for _, t := range registry {
		res = append(res, t)
	}
	return res
}

// GetPolicy 查取事件类型的当前级别和处置策略。若类型未注册，使用兜底 (info, store)。
func GetPolicy(eventType string) (severity, disposition string) {
	policyMu.RLock()
	p, ok := policies[eventType]
	policyMu.RUnlock()
	if ok {
		return p.Severity, p.Disposition
	}
	return "info", "store"
}

// UpdatePolicyCache 更新内存中的策略缓存。
func UpdatePolicyCache(eventType, severity, disposition string) {
	policyMu.Lock()
	defer policyMu.Unlock()
	policies[eventType] = TypePolicy{
		Severity:    severity,
		Disposition: disposition,
	}
}

// ResetPolicies 重置策略缓存为注册表初始默认值。
func ResetPolicies() {
	registryMu.RLock()
	defer registryMu.RUnlock()
	policyMu.Lock()
	defer policyMu.Unlock()
	policies = make(map[string]TypePolicy, len(registry))
	for k, v := range registry {
		policies[k] = TypePolicy{
			Severity:    v.Severity,
			Disposition: v.Disposition,
		}
	}
}

func init() {
	// 第一期六种内置事件类型 (P1-20 §第一期要接入的事件)
	RegisterType(TypeDef{
		Type:               "node.online",
		DisplayName:        "节点上线",
		DefaultSeverity:    "info",
		DefaultDisposition: "store",
		Description:        "节点建立连接并恢复心跳通信",
	})
	RegisterType(TypeDef{
		Type:               "node.offline",
		DisplayName:        "节点离线",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store+notify",
		Description:        "节点心跳超时或非正常断开连接",
	})
	RegisterType(TypeDef{
		Type:               "node.enrolled",
		DisplayName:        "新节点接入",
		DefaultSeverity:    "info",
		DefaultDisposition: "store",
		Description:        "新节点使用 enrollment token 完成初始认证接入",
	})
	RegisterType(TypeDef{
		Type:               "node.removed",
		DisplayName:        "节点删除",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store",
		Description:        "节点从平台中被管理员主动移除",
	})
	RegisterType(TypeDef{
		Type:               "auth.login_failed",
		DisplayName:        "登录失败",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store",
		Description:        "控制台用户凭据错误或认证失败",
	})
	RegisterType(TypeDef{
		Type:               "system.db_unreachable",
		DisplayName:        "数据库不可达",
		DefaultSeverity:    "critical",
		DefaultDisposition: "store+notify",
		Description:        "服务端数据库连接中断或数据批量落库失败",
	})
}
