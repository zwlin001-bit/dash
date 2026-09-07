package linux

import (
	"path/filepath"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewMemCollector())
}

// MemCollector 采集内存与 Swap 使用量。
type MemCollector struct {
	reader *procReader
	buf    []byte
}

// NewMemCollector 创建默认内存采集器。
func NewMemCollector() *MemCollector {
	return NewMemCollectorWithRoot(GetProcRoot())
}

// NewMemCollectorWithRoot 创建指定 proc 根目录的内存采集器。
func NewMemCollectorWithRoot(root string) *MemCollector {
	return &MemCollector{
		reader: newProcReader(filepath.Join(root, "meminfo")),
		buf:    make([]byte, 2048),
	}
}

func (c *MemCollector) Code() string {
	return "mem"
}

func (c *MemCollector) Tier() collect.Tier {
	return collect.TierFast
}

// Collect 采样 /proc/meminfo。
func (c *MemCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	var (
		memTotal     uint64
		memFree      uint64
		memAvailable uint64
		buffers      uint64
		cached       uint64
		swapTotal    uint64
		swapFree     uint64

		hasMemTotal     bool
		hasMemFree      bool
		hasMemAvailable bool
		hasBuffers      bool
		hasCached       bool
		hasSwapTotal    bool
		hasSwapFree     bool
	)

	rem := data
	for len(rem) > 0 {
		lineEnd := -1
		for i, b := range rem {
			if b == '\n' {
				lineEnd = i
				break
			}
		}
		var line []byte
		if lineEnd >= 0 {
			line = rem[:lineEnd]
			rem = rem[lineEnd+1:]
		} else {
			line = rem
			rem = nil
		}

		colon := -1
		for i, b := range line {
			if b == ':' {
				colon = i
				break
			}
		}
		if colon < 0 {
			continue
		}

		key := line[:colon]
		valBytes := line[colon+1:]

		val, ok := parseUint(valBytes)
		if !ok {
			continue
		}

		switch string(key) {
		case "MemTotal":
			memTotal = val
			hasMemTotal = true
		case "MemFree":
			memFree = val
			hasMemFree = true
		case "MemAvailable":
			memAvailable = val
			hasMemAvailable = true
		case "Buffers":
			buffers = val
			hasBuffers = true
		case "Cached":
			cached = val
			hasCached = true
		case "SwapTotal":
			swapTotal = val
			hasSwapTotal = true
		case "SwapFree":
			swapFree = val
			hasSwapFree = true
		}
	}

	if !hasMemTotal {
		// 没有 MemTotal 则无法计算内存
		return nil
	}

	var memUsedKB int64
	if hasMemAvailable {
		memUsedKB = int64(memTotal) - int64(memAvailable)
	} else if hasMemFree {
		// Alpine 或极老内核缺少 MemAvailable 时的回退
		used := int64(memTotal) - int64(memFree)
		if hasBuffers {
			used -= int64(buffers)
		}
		if hasCached {
			used -= int64(cached)
		}
		memUsedKB = used
	} else {
		return nil
	}

	if memUsedKB < 0 {
		memUsedKB = 0
	}

	var swapUsedKB int64
	hasSwap := false
	if hasSwapTotal && swapTotal > 0 && hasSwapFree {
		swapUsedKB = int64(swapTotal) - int64(swapFree)
		if swapUsedKB < 0 {
			swapUsedKB = 0
		}
		hasSwap = true
	} else if hasSwapTotal && swapTotal == 0 {
		swapUsedKB = 0
		hasSwap = true
	}

	sample.SetMem(memUsedKB*1024, swapUsedKB*1024, hasSwap)
	return nil
}

// Close 释放文件描述符。
func (c *MemCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
