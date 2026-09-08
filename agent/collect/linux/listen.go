package linux

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewListenCollector())
}

// ListenCollector 采集系统监听端口清单及对应的进程名。
// 直读 /proc/net/tcp 与 /proc/net/tcp6，严禁起子进程调 ss/netstat/lsof。
type ListenCollector struct {
	procPath string
	Enabled  bool
}

// NewListenCollector 创建默认 Listen 采集器。
func NewListenCollector() *ListenCollector {
	return NewListenCollectorWithRoot(GetProcRoot())
}

// NewListenCollectorWithRoot 创建指定 proc 根目录的 Listen 采集器。
func NewListenCollectorWithRoot(root string) *ListenCollector {
	return &ListenCollector{
		procPath: root,
		Enabled:  true,
	}
}

func (c *ListenCollector) Code() string {
	return "listen"
}

func (c *ListenCollector) Tier() collect.Tier {
	return collect.TierSlow
}

type socketMeta struct {
	local string
	inode string
	port  uint64
	isV6  bool
}

// CollectPayload 执行监听端口采集并返回独立 payload。
func (c *ListenCollector) CollectPayload() (*collect.ListenPayload, error) {
	if !c.Enabled {
		return nil, nil
	}

	netDir := filepath.Join(c.procPath, "net")
	tcpPath := filepath.Join(netDir, "tcp")
	tcp6Path := filepath.Join(netDir, "tcp6")

	v4Sockets, err4 := parseTCPProcFile(tcpPath, false)
	if err4 != nil {
		return nil, fmt.Errorf("read %s: %w", tcpPath, err4)
	}

	// 记录 IPv4 已经占用的端口，用于识别 IPv6 双栈通配通配监听
	v4Ports := make(map[uint64]bool, len(v4Sockets))
	for _, s := range v4Sockets {
		v4Ports[s.port] = true
	}

	v6Sockets, _ := parseTCPProcFile(tcp6Path, true)

	totalLen := len(v4Sockets) + len(v6Sockets)
	entries := make([]collect.ListenEntry, 0, totalLen)
	inodeMap := make(map[string][]int, totalLen)

	for _, s := range v4Sockets {
		idx := len(entries)
		entries = append(entries, collect.ListenEntry{
			Local: s.local,
			Proc:  "",
		})
		if s.inode != "" && s.inode != "0" {
			inodeMap[s.inode] = append(inodeMap[s.inode], idx)
		}
	}

	for _, s := range v6Sockets {
		localStr := s.local
		// 若为 IPv6 IN6ADDR_ANY (::)，且未在 IPv4 tcp 出现同端口，则表示双栈通配（与 ss 的 *:port 对齐）
		if strings.HasPrefix(s.local, "[::]:") {
			if !v4Ports[s.port] {
				localStr = fmt.Sprintf("*:%d", s.port)
			}
		}

		idx := len(entries)
		entries = append(entries, collect.ListenEntry{
			Local: localStr,
			Proc:  "",
		})
		if s.inode != "" && s.inode != "0" {
			inodeMap[s.inode] = append(inodeMap[s.inode], idx)
		}
	}

	// 若存在待解析 inode，扫描 /proc 下进程 fd 查找 socket 关联
	if len(inodeMap) > 0 {
		resolveProcesses(c.procPath, inodeMap, entries)
	}

	return &collect.ListenPayload{Listen: entries}, nil
}

// Collect 执行采集并将结果写入 sample。
func (c *ListenCollector) Collect(sample *collect.Sample) error {
	payload, err := c.CollectPayload()
	if err != nil {
		return err
	}
	if payload != nil {
		sample.SetListen(payload)
	}
	return nil
}

func parseTCPProcFile(path string, isV6 bool) ([]socketMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if isV6 && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var res []socketMeta
	scanner := bufio.NewScanner(bytes.NewReader(data))
	isFirstLine := true

	for scanner.Scan() {
		line := scanner.Bytes()
		if isFirstLine {
			isFirstLine = false
			continue
		}

		fields := bytes.Fields(line)
		if len(fields) < 10 {
			continue
		}

		// fields[3] 为 socket 状态，0A 表示 TCP_LISTEN
		st := string(fields[3])
		if st != "0A" {
			continue
		}

		localRaw := string(fields[1])
		inode := string(fields[9])

		colonIdx := strings.IndexByte(localRaw, ':')
		if colonIdx < 0 {
			continue
		}
		ipHex := localRaw[:colonIdx]
		portHex := localRaw[colonIdx+1:]

		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}

		var localFormatted string
		if isV6 {
			formatted, ok := parseIPv6Hex(ipHex)
			if !ok {
				continue
			}
			localFormatted = fmt.Sprintf("[%s]:%d", formatted, port)
		} else {
			formatted, ok := parseIPv4Hex(ipHex)
			if !ok {
				continue
			}
			localFormatted = fmt.Sprintf("%s:%d", formatted, port)
		}

		res = append(res, socketMeta{
			local: localFormatted,
			inode: inode,
			port:  port,
			isV6:  isV6,
		})
	}

	return res, nil
}

func parseIPv4Hex(h string) (string, bool) {
	if len(h) != 8 {
		return "", false
	}
	val, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return "", false
	}
	// Linux /proc/net/tcp 存储格式为小端序无符号 32 位整型
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(val))
	ip := net.IPv4(b[0], b[1], b[2], b[3])
	return ip.String(), true
}

func parseIPv6Hex(h string) (string, bool) {
	if len(h) != 32 {
		return "", false
	}
	// Linux /proc/net/tcp6 存储格式为 4 个小端序 32 位整数串联
	var ipBytes [16]byte
	for i := 0; i < 4; i++ {
		wordHex := h[i*8 : (i+1)*8]
		val, err := strconv.ParseUint(wordHex, 16, 32)
		if err != nil {
			return "", false
		}
		binary.LittleEndian.PutUint32(ipBytes[i*4:(i+1)*4], uint32(val))
	}
	ip := net.IP(ipBytes[:])
	return ip.String(), true
}

func resolveProcesses(procPath string, inodeMap map[string][]int, entries []collect.ListenEntry) {
	entriesDir, err := os.ReadDir(procPath)
	if err != nil {
		return
	}

	for _, entry := range entriesDir {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}
		// 验证是否全数字
		isNum := true
		for i := 1; i < len(name); i++ {
			if name[i] < '0' || name[i] > '9' {
				isNum = false
				break
			}
		}
		if !isNum {
			continue
		}

		pidPath := filepath.Join(procPath, name)
		fdDir := filepath.Join(pidPath, "fd")

		fds, err := os.ReadDir(fdDir)
		if err != nil {
			// 非 root 运行时其他用户进程的 /proc/<pid>/fd 无权限访问，静默忽略
			continue
		}

		var comm string
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}

			if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
				inode := link[8 : len(link)-1]
				if targetIndices, ok := inodeMap[inode]; ok {
					if comm == "" {
						commBytes, _ := os.ReadFile(filepath.Join(pidPath, "comm"))
						comm = strings.TrimSpace(string(commBytes))
					}
					for _, idx := range targetIndices {
						entries[idx].Proc = comm
					}
					delete(inodeMap, inode)
					if len(inodeMap) == 0 {
						return
					}
				}
			}
		}
	}
}
