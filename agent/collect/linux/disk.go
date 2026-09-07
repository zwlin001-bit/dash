package linux

import (
	"path/filepath"
	"strings"
	"syscall"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewDiskCollector())
}

type StatfsFunc func(path string) (total, used int64, err error)

func defaultStatfs(path string) (total, used int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	t := int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bfree) * int64(st.Bsize)
	u := t - free
	if u < 0 {
		u = 0
	}
	return t, u, nil
}

// DiskCollector 采集磁盘挂载点用量与总已用。
type DiskCollector struct {
	reader        *procReader
	buf           []byte
	Statfs        StatfsFunc
	IncludeMounts []string
	ExcludeMounts []string
}

// NewDiskCollector 创建默认磁盘采集器。
func NewDiskCollector() *DiskCollector {
	return NewDiskCollectorWithRoot(GetProcRoot())
}

// NewDiskCollectorWithRoot 创建指定 proc 根目录的磁盘采集器。
func NewDiskCollectorWithRoot(root string) *DiskCollector {
	return &DiskCollector{
		reader: newProcReader(filepath.Join(root, "mounts")),
		buf:    make([]byte, 8192),
		Statfs: defaultStatfs,
	}
}

func (c *DiskCollector) Code() string {
	return "disk"
}

func (c *DiskCollector) Tier() collect.Tier {
	return collect.TierSlow
}

func (c *DiskCollector) isExcludedFS(fsType, mountPoint string) bool {
	if len(c.IncludeMounts) > 0 {
		for _, inc := range c.IncludeMounts {
			if mountPoint == inc || strings.HasPrefix(mountPoint, inc) {
				return false
			}
		}
		return true
	}

	for _, exc := range c.ExcludeMounts {
		if mountPoint == exc || strings.HasPrefix(mountPoint, exc) {
			return true
		}
	}

	// 默认排除类型
	switch fsType {
	case "tmpfs", "devtmpfs", "overlay", "squashfs", "proc", "sysfs",
		"devpts", "securityfs", "pstore", "bpf", "autofs", "binfmt_misc",
		"configfs", "debugfs", "tracefs", "fusectl", "efivarfs", "mqueue",
		"hugetlbfs", "none", "cgroup", "cgroup2", "ramfs", "nsfs":
		return true
	}

	if strings.HasPrefix(fsType, "cgroup") || strings.HasPrefix(mountPoint, "/proc") || strings.HasPrefix(mountPoint, "/sys") || strings.HasPrefix(mountPoint, "/dev") {
		return true
	}

	return false
}

// Collect 采样 /proc/mounts 并调用 statfs 获取容量。
func (c *DiskCollector) Collect(sample *collect.Sample) error {
	data, err := c.reader.Read(c.buf)
	if err != nil {
		return err
	}

	var diskUsedSum int64
	seenMounts := make(map[string]struct{})

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

		// 格式: device mountpoint fstype options dump pass
		_, rest := nextField(line)
		if rest == nil {
			continue
		}
		mountField, rest := nextField(rest)
		if mountField == nil || rest == nil {
			continue
		}
		fstypeField, _ := nextField(rest)
		if fstypeField == nil {
			continue
		}

		mountPoint := string(mountField)
		fsType := string(fstypeField)

		if c.isExcludedFS(fsType, mountPoint) {
			continue
		}

		if _, seen := seenMounts[mountPoint]; seen {
			continue
		}
		seenMounts[mountPoint] = struct{}{}

		total, used, statErr := c.Statfs(mountPoint)
		if statErr != nil || total <= 0 {
			continue
		}

		diskUsedSum += used
		sample.AddDisk(mountPoint, used, total)
	}

	if len(seenMounts) > 0 {
		sample.SetDiskUsed(diskUsedSum)
	}
	return nil
}

// Close 释放文件描述符。
func (c *DiskCollector) Close() {
	if c.reader != nil {
		c.reader.Close()
	}
}
