package billing

import "dash/internal/events"

func init() {
	events.RegisterType(events.TypeDef{
		Type:               "billing.budget_warning",
		DisplayName:        "云账单预算预警",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store+notify",
		Description:        "当月云账单用量达到预算预警阈值",
	})
	events.RegisterType(events.TypeDef{
		Type:               "billing.budget_exceeded",
		DisplayName:        "云账单超出预算",
		DefaultSeverity:    "critical",
		DefaultDisposition: "store+notify",
		Description:        "当月云账单用量已超出设定预算",
	})
	events.RegisterType(events.TypeDef{
		Type:               "billing.sync_failed",
		DisplayName:        "云账单同步失败",
		DefaultSeverity:    "error",
		DefaultDisposition: "store+notify",
		Description:        "云厂商账单数据同步失败",
	})
}
