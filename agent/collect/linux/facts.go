package linux

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"dash/agent/collect"
	"dash/internal/protocol"
)

func init() {
	collect.Register(NewFactsCollector())
}

// FactsCollector 采集系统与硬件静态信息。
type FactsCollector struct {
	procRoot string
	etcRoot  string
	sysRoot  string
	Statfs   StatfsFunc
	IPLookup func() (ipv4 string, ipv6 string)
}

// NewFactsCollector 创建默认 Facts 采集器。
func NewFactsCollector() *FactsCollector {
	return NewFactsCollectorWithRoots(GetProcRoot(), "/etc", "/sys")
}

// NewFactsCollectorWithRoots 创建支持自定义路径的 Facts 采集器（供单元测试）。
func NewFactsCollectorWithRoots(procRoot, etcRoot, sysRoot string) *FactsCollector {
	return &FactsCollector{
		procRoot: procRoot,
		etcRoot:  etcRoot,
		sysRoot:  sysRoot,
		Statfs:   defaultStatfs,
		IPLookup: defaultIPLookup,
	}
}

func (c *FactsCollector) Code() string {
	return "facts"
}

func (c *FactsCollector) Tier() collect.Tier {
	return collect.TierFacts
}

func defaultIPLookup() (string, string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}

	var ipv4, ipv6 string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				if ipv4 == "" {
					ipv4 = ip4.String()
				}
			} else if ip.To16() != nil {
				if ipv6 == "" && ip.IsGlobalUnicast() {
					ipv6 = ip.String()
				}
			}
		}
	}
	return ipv4, ipv6
}

func (c *FactsCollector) Collect(sample *collect.Sample) error {
	facts := protocol.FactsParams{
		Arch: runtime.GOARCH,
	}

	// 1. OS 名称与版本从 os-release 读取
	osName, osVer := c.readOSRelease()
	facts.OSName = osName
	facts.OSVersion = osVer

	// 2. Kernel 版本
	facts.Kernel = c.readKernel()

	// 3. 虚拟化类型
	facts.Virt = c.detectVirt()

	// 4. CPU 模型、物理核心数、逻辑线程数
	cpuModel, cores, threads := c.readCPUInfo()
	facts.CPUModel = cpuModel
	facts.CPUCores = cores
	facts.CPUThreads = threads

	// 5. 内存总量与 Swap 总量
	memTotal, swapTotal := c.readMemTotal()
	facts.MemTotal = memTotal
	facts.SwapTotal = swapTotal

	// 6. 磁盘总量
	facts.DiskTotal = c.readDiskTotal()

	// 7. IP 地址
	if c.IPLookup != nil {
		facts.IPv4, facts.IPv6 = c.IPLookup()
	}

	// 8. 开机时间 (btime)
	facts.BootAtMs = c.readBootAtMs()

	// 9. 计算 facts_hash（除 boot_at_ms 外的所有字段按固定顺序拼接后 sha256）
	facts.FactsHash = ComputeFactsHash(facts)

	sample.SetFacts(facts)
	return nil
}

// ComputeFactsHash 拼接静态字段计算哈希（不含 boot_at_ms）。
func ComputeFactsHash(f protocol.FactsParams) string {
	raw := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d|%d|%d|%d|%s|%s",
		f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt,
		f.CPUModel, f.CPUCores, f.CPUThreads,
		f.MemTotal, f.SwapTotal, f.DiskTotal,
		f.IPv4, f.IPv6,
	)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16]) // 32 hex chars
}

func (c *FactsCollector) readOSRelease() (string, string) {
	paths := []string{
		filepath.Join(c.etcRoot, "os-release"),
		filepath.Join(c.etcRoot, "usr", "lib", "os-release"),
		"/usr/lib/os-release",
	}

	var content []byte
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			content = data
			break
		}
	}

	var osName, osVer string
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			osName = strings.Trim(strings.TrimPrefix(line, "ID="), `"`)
		} else if strings.HasPrefix(line, "VERSION_ID=") {
			osVer = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), `"`)
		}
	}
	return osName, osVer
}

func (c *FactsCollector) readKernel() string {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		var buf []byte
		for _, b := range uts.Release {
			if b == 0 {
				break
			}
			buf = append(buf, byte(b))
		}
		if len(buf) > 0 {
			return string(buf)
		}
	}

	data, err := os.ReadFile(filepath.Join(c.procRoot, "sys", "kernel", "osrelease"))
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

func (c *FactsCollector) detectVirt() string {
	if os.Getenv("HOST_PROC") != "" {
		return "container"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "container"
	}
	if _, err := os.Stat("/run/systemd/container"); err == nil {
		return "container"
	}

	// 检查 DMI 信息
	dmiProduct, _ := os.ReadFile(filepath.Join(c.sysRoot, "class", "dmi", "id", "product_name"))
	dmiVendor, _ := os.ReadFile(filepath.Join(c.sysRoot, "class", "dmi", "id", "sys_vendor"))
	combined := strings.ToLower(string(dmiProduct) + " " + string(dmiVendor))

	if strings.Contains(combined, "kvm") || strings.Contains(combined, "qemu") || strings.Contains(combined, "bochs") {
		return "kvm"
	}
	if strings.Contains(combined, "vmware") {
		return "vmware"
	}
	if strings.Contains(combined, "virtualbox") {
		return "virtualbox"
	}
	if strings.Contains(combined, "xen") {
		return "xen"
	}
	if strings.Contains(combined, "microsoft") {
		return "hyperv"
	}

	// 检查 cpuinfo 是否带 hypervisor
	cpuinfo, _ := os.ReadFile(filepath.Join(c.procRoot, "cpuinfo"))
	if strings.Contains(string(cpuinfo), "hypervisor") {
		return "kvm"
	}

	return "baremetal"
}

func (c *FactsCollector) readCPUInfo() (string, int32, int32) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "cpuinfo"))
	if err != nil {
		return "", 1, 1
	}

	var model string
	var threads int32
	coreIDSet := make(map[string]struct{})

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "model name", "Hardware", "Processor":
			if model == "" {
				model = val
			}
		case "processor":
			threads++
		case "core id":
			coreIDSet[val] = struct{}{}
		}
	}

	if threads == 0 {
		threads = 1
	}
	cores := int32(len(coreIDSet))
	if cores == 0 {
		cores = threads
	}

	return model, cores, threads
}

func (c *FactsCollector) readMemTotal() (int64, int64) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "meminfo"))
	if err != nil {
		return 0, 0
	}

	var memTotal, swapTotal int64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valBytes := strings.TrimSpace(parts[1])
		valBytes = strings.TrimSuffix(valBytes, " kB")
		val, _ := parseInt([]byte(valBytes))

		if key == "MemTotal" {
			memTotal = val * 1024
		} else if key == "SwapTotal" {
			swapTotal = val * 1024
		}
	}
	return memTotal, swapTotal
}

func (c *FactsCollector) readDiskTotal() int64 {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "mounts"))
	if err != nil {
		return 0
	}

	diskCollector := &DiskCollector{Statfs: c.Statfs}
	var totalDisk int64
	seenMounts := make(map[string]struct{})
	seenDevs := make(map[uint64]struct{})

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountPoint := fields[1]
		fsType := fields[2]

		if diskCollector.isExcludedFS(fsType, mountPoint) {
			continue
		}
		if _, seen := seenMounts[mountPoint]; seen {
			continue
		}
		seenMounts[mountPoint] = struct{}{}

		total, _, dev, statErr := c.Statfs(mountPoint)
		if statErr == nil && total > 0 {
			if dev != 0 {
				if _, seen := seenDevs[dev]; seen {
					continue
				}
				seenDevs[dev] = struct{}{}
			}
			totalDisk += total
		}
	}
	return totalDisk
}

func (c *FactsCollector) readBootAtMs() int64 {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "stat"))
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				btime, ok := parseInt([]byte(fields[1]))
				if ok {
					return btime * 1000
				}
			}
		}
	}
	return 0
}
