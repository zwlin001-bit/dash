package linux

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewToolParamCollector())
}

var kvLineRegex = regexp.MustCompile(`^\s*([A-Z_][A-Z0-9_]*)\s*=`)

// ToolParamCollector 采集本地各工具配置文件中的实际生效参数（用于期望态对比与自愈感知）。
type ToolParamCollector struct {
	toolRoot string
	Enabled  bool
}

// NewToolParamCollector 创建默认工具参数采集器（以 /opt/tool 为根目录）。
func NewToolParamCollector() *ToolParamCollector {
	return NewToolParamCollectorWithRoot("/opt/tool")
}

// NewToolParamCollectorWithRoot 创建指定 toolRoot 的工具参数采集器。
func NewToolParamCollectorWithRoot(root string) *ToolParamCollector {
	return &ToolParamCollector{
		toolRoot: root,
		Enabled:  true,
	}
}

func (c *ToolParamCollector) Code() string {
	return "tool_param"
}

func (c *ToolParamCollector) Tier() collect.Tier {
	return collect.TierSlow
}

// CollectPayload 执行工具参数采集并返回键值字典映射。
func (c *ToolParamCollector) CollectPayload() (map[string]map[string]string, error) {
	if !c.Enabled {
		return nil, nil
	}

	res := make(map[string]map[string]string)

	// 1. network 工具配置
	netConfPath := filepath.Join(c.toolRoot, "network", "config.conf")
	if isRegularFile(netConfPath) {
		netKV, err := parseConfigFileKV(netConfPath)
		if err != nil {
			return nil, err
		}
		res["network"] = netKV
	}

	// 2. podguard 工具配置（podguard.conf 基础配置 + podguard.local.conf 本地覆盖）
	pgKV := make(map[string]string)
	hasPG := false
	pgPaths := []string{
		filepath.Join(c.toolRoot, "podguard", "config", "podguard.conf"),
		filepath.Join(c.toolRoot, "podguard", "config", "podguard.local.conf"),
	}
	for _, p := range pgPaths {
		if isRegularFile(p) {
			hasPG = true
			kv, err := parseConfigFileKV(p)
			if err != nil {
				return nil, err
			}
			for k, v := range kv {
				pgKV[k] = v
			}
		}
	}
	if hasPG && len(pgKV) > 0 {
		res["podguard"] = pgKV
	}

	return res, nil
}

// Collect 执行采集并将结果设置到 sample。
func (c *ToolParamCollector) Collect(sample *collect.Sample) error {
	payload, err := c.CollectPayload()
	if err != nil {
		return err
	}
	if payload != nil {
		sample.SetToolParam(payload)
	}
	return nil
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}

func parseConfigFileKV(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	res := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		loc := kvLineRegex.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}

		key := line[loc[2]:loc[3]]
		valRaw := strings.TrimSpace(line[loc[1]:])
		val := strings.Trim(valRaw, `"'`)

		res[key] = val
	}

	return res, nil
}
