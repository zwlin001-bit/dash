package linux

import (
	"path/filepath"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewUptimeCollector())
}

// UptimeCollector 采集系统运行时间（秒）。
type UptimeCollector struct {
	reader *procReader
	buf    []byte
}

// NewUptimeCollector 创建默认 Uptime 采集器。
func NewUptimeCollector() *UptimeCollector {
	return NewUptimeCollectorWithRoot(GetProcRoot())
}

// NewUptimeCollectorWithRoot 创建指定 proc 根目录的 Uptime 采集器。
func NewUptimeCollectorWithRoot(root string) *UptimeCollector {
	return &UptimeCollector{
		reader: newProcReader(filepath.Join(root, "uptime")),
		buf:    make([]byte, 128),
	}
}

func (c *UptimeCollector) Code() string {
	return "uptime"
}

func (c *UptimeCollector) Tier() collect.Tier {
	return collect.TierFast
}

// Collect 采样 /proc/uptime。
func (c *UptimeCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	field, _ := nextField(data)
	if field == nil {
		return nil
	}

	sec, ok := parseFloat(field)
	if !ok {
		return nil
	}

	sample.SetUptime(int64(sec))
	return nil
}

// Close 释放文件描述符。
func (c *UptimeCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
