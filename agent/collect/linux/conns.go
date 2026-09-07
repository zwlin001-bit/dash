package linux

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewConnsCollector())
}

var inuseBytes = []byte("inuse")

// ConnsCollector 采集 TCP 与 UDP 连接数。
// 优先读取 /proc/net/sockstat 与 /proc/net/sockstat6 获取 inuse 连接数；
// 缺失时回退到统计 /proc/net/tcp 与 udp 行数并记一次日志。
type ConnsCollector struct {
	procPath       string
	buf            []byte
	reader4        *procReader
	reader6        *procReader
	fallbackLogged bool
	Enabled        bool
}

// NewConnsCollector 创建默认连接数采集器。
func NewConnsCollector() *ConnsCollector {
	return NewConnsCollectorWithRoot(GetProcRoot())
}

// NewConnsCollectorWithRoot 创建指定 proc 根目录的连接数采集器。
func NewConnsCollectorWithRoot(root string) *ConnsCollector {
	netDir := filepath.Join(root, "net")
	return &ConnsCollector{
		procPath: root,
		buf:      make([]byte, 8192),
		reader4:  newProcReader(filepath.Join(netDir, "sockstat")),
		reader6:  newProcReader(filepath.Join(netDir, "sockstat6")),
		Enabled:  true,
	}
}

func (c *ConnsCollector) Code() string {
	return "conns"
}

func (c *ConnsCollector) Tier() collect.Tier {
	return collect.TierSlow
}

// parseSockstatInuse 从 sockstat 内容中查找指定协议行，并提取 "inuse" 后的整数值。
func parseSockstatInuse(buf []byte, prefix []byte) int32 {
	line := findLine(buf, prefix)
	if line == nil {
		return 0
	}
	rem := line
	for len(rem) > 0 {
		var tok []byte
		tok, rem = nextField(rem)
		if bytes.Equal(tok, inuseBytes) {
			valTok, _ := nextField(rem)
			if valTok != nil {
				val, ok := parseUint(valTok)
				if ok {
					return int32(val)
				}
			}
			return 0
		}
	}
	return 0
}

func countLinesInFile(path string, buf []byte) (int32, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) || err == syscall.ENOENT {
			return 0, nil
		}
		return 0, err
	}
	defer syscall.Close(fd)

	var lines int32
	for {
		n, readErr := syscall.Read(fd, buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					lines++
				}
			}
		}
		if readErr != nil || n == 0 {
			break
		}
	}

	// 减去首行表头
	if lines > 0 {
		return lines - 1, nil
	}
	return 0, nil
}

// Collect 读取 /proc/net/sockstat[6] 或回退读取 tcp[6]/udp[6]。
func (c *ConnsCollector) Collect(sample *collect.Sample) error {
	if !c.Enabled {
		return nil
	}

	data4, err4 := c.reader4.Read(c.buf)
	if err4 != nil {
		// 回退逻辑：读取 /proc/net/tcp 与 udp 行数
		if !c.fallbackLogged {
			log.Printf("dash-agent: /proc/net/sockstat missing, falling back to /proc/net/tcp")
			c.fallbackLogged = true
		}
		netDir := filepath.Join(c.procPath, "net")
		tcp4, _ := countLinesInFile(filepath.Join(netDir, "tcp"), c.buf)
		tcp6, _ := countLinesInFile(filepath.Join(netDir, "tcp6"), c.buf)
		udp4, _ := countLinesInFile(filepath.Join(netDir, "udp"), c.buf)
		udp6, _ := countLinesInFile(filepath.Join(netDir, "udp6"), c.buf)

		sample.SetTCPCount(tcp4 + tcp6)
		sample.SetUDPCount(udp4 + udp6)
		return nil
	}

	// 解析 IPv4 sockstat
	tcp4 := parseSockstatInuse(data4, []byte("TCP:"))
	udp4 := parseSockstatInuse(data4, []byte("UDP:"))

	// 解析 IPv6 sockstat6（纯 IPv4 环境下不存在时按 0 处理，不报错）
	var tcp6, udp6 int32
	data6, err6 := c.reader6.Read(c.buf)
	if err6 == nil {
		tcp6 = parseSockstatInuse(data6, []byte("TCP6:"))
		udp6 = parseSockstatInuse(data6, []byte("UDP6:"))
	}

	sample.SetTCPCount(tcp4 + tcp6)
	sample.SetUDPCount(udp4 + udp6)
	return nil
}

// Close 释放内部 procReader 的文件描述符。
func (c *ConnsCollector) Close() {
	if c.reader4 != nil {
		c.reader4.Close()
	}
	if c.reader6 != nil {
		c.reader6.Close()
	}
}
