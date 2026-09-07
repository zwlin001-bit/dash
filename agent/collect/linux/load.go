package linux

import (
	"path/filepath"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewLoadCollector())
}

// LoadCollector 采集系统负载 (1m, 5m, 15m)。
type LoadCollector struct {
	reader *procReader
	buf    []byte
}

// NewLoadCollector 创建默认负载采集器。
func NewLoadCollector() *LoadCollector {
	return NewLoadCollectorWithRoot(GetProcRoot())
}

// NewLoadCollectorWithRoot 创建指定 proc 根目录的负载采集器。
func NewLoadCollectorWithRoot(root string) *LoadCollector {
	return &LoadCollector{
		reader: newProcReader(filepath.Join(root, "loadavg")),
		buf:    make([]byte, 128),
	}
}

func (c *LoadCollector) Code() string {
	return "load"
}

func (c *LoadCollector) Tier() collect.Tier {
	return collect.TierFast
}

// Collect 采样 /proc/loadavg。
func (c *LoadCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	var l [3]float64
	rem := data
	for i := 0; i < 3; i++ {
		field, rest := nextField(rem)
		if field == nil {
			return nil
		}
		val, ok := parseFloat(field)
		if !ok {
			return nil
		}
		l[i] = val
		rem = rest
	}

	sample.SetLoad(l)
	return nil
}

// Close 释放文件描述符。
func (c *LoadCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
