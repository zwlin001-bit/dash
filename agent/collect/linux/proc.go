package linux

import (
	"os"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewProcCollector())
}

// ProcCollector 采集系统进程总数。
type ProcCollector struct {
	procPath string
}

// NewProcCollector 创建默认进程数采集器。
func NewProcCollector() *ProcCollector {
	return NewProcCollectorWithRoot(GetProcRoot())
}

// NewProcCollectorWithRoot 创建指定 proc 根目录的进程数采集器。
func NewProcCollectorWithRoot(root string) *ProcCollector {
	return &ProcCollector{
		procPath: root,
	}
}

func (c *ProcCollector) Code() string {
	return "proc"
}

func (c *ProcCollector) Tier() collect.Tier {
	return collect.TierSlow
}

func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Collect 读取 /proc 目录下所有由纯数字命名的子目录（代表每个进程）。
func (c *ProcCollector) Collect(sample *collect.Sample) error {
	dir, err := os.Open(c.procPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}

	var count int32
	for _, name := range names {
		if isAllDigits(name) {
			count++
		}
	}

	sample.SetProcCount(count)
	return nil
}
