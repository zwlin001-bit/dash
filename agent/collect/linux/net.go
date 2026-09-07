package linux

import (
	"path/filepath"
	"strings"
	"time"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewNetCollector())
}

// NetCollector 采集网络聚合流量与单网卡累计流量。
type NetCollector struct {
	reader      *procReader
	buf         []byte
	prevTime    time.Time
	prevRx      uint64
	prevTx      uint64
	hasPrev     bool
	IncludeNICs []string
	ExcludeNICs []string
	nameCache   map[string]string
}

// NewNetCollector 创建默认网络采集器。
func NewNetCollector() *NetCollector {
	return NewNetCollectorWithRoot(GetProcRoot())
}

// NewNetCollectorWithRoot 创建指定 proc 根目录的网络采集器。
func NewNetCollectorWithRoot(root string) *NetCollector {
	return &NetCollector{
		reader:    newProcReader(filepath.Join(root, "net", "dev")),
		buf:       make([]byte, 4096),
		nameCache: make(map[string]string),
	}
}

func (c *NetCollector) Code() string {
	return "net"
}

func (c *NetCollector) Tier() collect.Tier {
	return collect.TierFast
}

// isExcludedNIC 检查网卡是否在排除名单中。
func (c *NetCollector) isExcludedNIC(name string) bool {
	if len(c.IncludeNICs) > 0 {
		for _, inc := range c.IncludeNICs {
			if name == inc || strings.HasPrefix(name, inc) {
				return false
			}
		}
		return true
	}

	for _, exc := range c.ExcludeNICs {
		if name == exc || strings.HasPrefix(name, exc) {
			return true
		}
	}

	// 默认排除: lo, docker*, veth*, br-*, tun*, tap*, kube*
	if name == "lo" ||
		strings.HasPrefix(name, "docker") ||
		strings.HasPrefix(name, "veth") ||
		strings.HasPrefix(name, "br-") ||
		strings.HasPrefix(name, "tun") ||
		strings.HasPrefix(name, "tap") ||
		strings.HasPrefix(name, "kube") {
		return true
	}

	return false
}

// getCachedName 复用网卡名称字符串，避免每轮分配。
func (c *NetCollector) getCachedName(b []byte) string {
	if s, ok := c.nameCache[string(b)]; ok {
		return s
	}
	s := string(b)
	if len(c.nameCache) < 64 {
		c.nameCache[s] = s
	}
	return s
}

// Collect 采样 /proc/net/dev。
func (c *NetCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	now := time.Now()
	var totalRx uint64
	var totalTx uint64

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

		// 提取网卡名
		nameBytes := line[:colon]
		for len(nameBytes) > 0 && (nameBytes[0] == ' ' || nameBytes[0] == '\t') {
			nameBytes = nameBytes[1:]
		}
		for len(nameBytes) > 0 && (nameBytes[len(nameBytes)-1] == ' ' || nameBytes[len(nameBytes)-1] == '\t') {
			nameBytes = nameBytes[:len(nameBytes)-1]
		}
		name := c.getCachedName(nameBytes)
		if c.isExcludedNIC(name) {
			continue
		}

		// 解析数值字段：
		// 0: rx_bytes, 1: rx_packets, ... 8: tx_bytes
		remFields := line[colon+1:]
		var rxBytes uint64
		var txBytes uint64
		valid := true
		for i := 0; i <= 8; i++ {
			field, rest := nextField(remFields)
			if field == nil {
				valid = false
				break
			}
			if i == 0 {
				val, ok := parseUint(field)
				if !ok {
					valid = false
					break
				}
				rxBytes = val
			} else if i == 8 {
				val, ok := parseUint(field)
				if !ok {
					valid = false
					break
				}
				txBytes = val
			}
			remFields = rest
		}

		if valid {
			totalRx += rxBytes
			totalTx += txBytes
			// 仅在已初始化 Slow 报文（即本轮为 slow 采集或测试）时记录单网卡维度，避免 fast 周期冗余工作
			if sample.Slow != nil {
				sample.AddNIC(name, int64(txBytes), int64(rxBytes))
			}
		}
	}

	if !c.hasPrev {
		c.prevRx = totalRx
		c.prevTx = totalTx
		c.prevTime = now
		c.hasPrev = true
		sample.SetNet(0, 0, int64(totalTx), int64(totalRx), false)
		return nil
	}

	elapsed := now.Sub(c.prevTime).Seconds()
	var upBps int64
	var downBps int64

	if elapsed > 0 {
		if totalTx >= c.prevTx {
			upBps = int64(float64(totalTx-c.prevTx) / elapsed)
		}
		if totalRx >= c.prevRx {
			downBps = int64(float64(totalRx-c.prevRx) / elapsed)
		}
	}

	c.prevRx = totalRx
	c.prevTx = totalTx
	c.prevTime = now

	sample.SetNet(upBps, downBps, int64(totalTx), int64(totalRx), true)
	return nil
}

// Close 释放文件描述符。
func (c *NetCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
