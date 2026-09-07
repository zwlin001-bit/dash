package linux

import (
	"math"
	"path/filepath"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewCPUCollector())
}

// CPUCollector 采集 CPU 使用率（两次采样求差）。
type CPUCollector struct {
	reader    *procReader
	buf       []byte
	prevBusy  uint64
	prevTotal uint64
	hasPrev   bool
}

// NewCPUCollector 创建默认 CPU 采集器。
func NewCPUCollector() *CPUCollector {
	return NewCPUCollectorWithRoot(GetProcRoot())
}

// NewCPUCollectorWithRoot 创建指定 proc 根目录的 CPU 采集器。
func NewCPUCollectorWithRoot(root string) *CPUCollector {
	return &CPUCollector{
		reader: newProcReader(filepath.Join(root, "stat")),
		buf:    make([]byte, 1024),
	}
}

func (c *CPUCollector) Code() string {
	return "cpu"
}

func (c *CPUCollector) Tier() collect.Tier {
	return collect.TierFast
}

// Collect 采样 /proc/stat 中的 cpu 行。
func (c *CPUCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	line := findLine(data, []byte("cpu "))
	if line == nil {
		return nil
	}

	// 依次解析 cpu 行字段:
	// user, nice, system, idle, iowait, irq, softirq, steal
	var fields [8]uint64
	rem := line
	for i := 0; i < 8; i++ {
		field, rest := nextField(rem)
		if field == nil {
			break
		}
		val, ok := parseUint(field)
		if !ok {
			break
		}
		fields[i] = val
		rem = rest
	}

	user := fields[0]
	nice := fields[1]
	system := fields[2]
	idle := fields[3]
	iowait := fields[4]
	irq := fields[5]
	softirq := fields[6]
	steal := fields[7]

	busy := user + nice + system + irq + softirq + steal
	total := busy + idle + iowait

	if !c.hasPrev {
		c.prevBusy = busy
		c.prevTotal = total
		c.hasPrev = true
		return nil
	}

	deltaBusy := busy - c.prevBusy
	deltaTotal := total - c.prevTotal
	c.prevBusy = busy
	c.prevTotal = total

	if deltaTotal > 0 {
		pct := float64(deltaBusy) * 100.0 / float64(deltaTotal)
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}
		pct = math.Round(pct*100) / 100
		sample.SetCPUPct(pct)
	}

	return nil
}

// Close 释放文件描述符。
func (c *CPUCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
