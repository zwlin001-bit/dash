package linux

import (
	"os"
	"path/filepath"
	"syscall"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewConnsCollector())
}

// ConnsCollector 采集 TCP 与 UDP 连接数。
type ConnsCollector struct {
	procPath string
	buf      []byte
}

// NewConnsCollector 创建默认连接数采集器。
func NewConnsCollector() *ConnsCollector {
	return NewConnsCollectorWithRoot(GetProcRoot())
}

// NewConnsCollectorWithRoot 创建指定 proc 根目录的连接数采集器。
func NewConnsCollectorWithRoot(root string) *ConnsCollector {
	return &ConnsCollector{
		procPath: root,
		buf:      make([]byte, 8192),
	}
}

func (c *ConnsCollector) Code() string {
	return "conns"
}

func (c *ConnsCollector) Tier() collect.Tier {
	return collect.TierSlow
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

// Collect 读取 /proc/net/tcp[6] 与 /proc/net/udp[6] 的行数。
func (c *ConnsCollector) Collect(sample *collect.Sample) error {
	netDir := filepath.Join(c.procPath, "net")

	tcp4, _ := countLinesInFile(filepath.Join(netDir, "tcp"), c.buf)
	tcp6, _ := countLinesInFile(filepath.Join(netDir, "tcp6"), c.buf)
	udp4, _ := countLinesInFile(filepath.Join(netDir, "udp"), c.buf)
	udp6, _ := countLinesInFile(filepath.Join(netDir, "udp6"), c.buf)

	sample.SetTCPCount(tcp4 + tcp6)
	sample.SetUDPCount(udp4 + udp6)
	return nil
}
