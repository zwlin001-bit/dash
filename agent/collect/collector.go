package collect

// Tier 表示指标采集的频次分级。
type Tier int

const (
	// TierFast 快速采集档（默认 5 秒），微秒级开销。
	TierFast Tier = 1
	// TierSlow 慢速采集档（默认 60 秒），昂贵采集项（连接数、进程数、磁盘用量）。
	TierSlow Tier = 2
	// TierFacts 静态硬件与系统信息，仅在变更时或启动时上报。
	TierFacts Tier = 3
)

func (t Tier) String() string {
	switch t {
	case TierFast:
		return "fast"
	case TierSlow:
		return "slow"
	case TierFacts:
		return "facts"
	default:
		return "unknown"
	}
}

// Collector 是采集器的通用契约接口。
type Collector interface {
	Code() string
	Tier() Tier
	Collect(sample *Sample) error
}
