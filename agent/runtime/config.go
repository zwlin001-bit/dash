package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 定义 dash-agent 运行配置。
// 见 docs/03-agent.md §8 与 docs/agy/tasks/P1-08-agent主循环.md。
type Config struct {
	Endpoint           string   `json:"endpoint"`
	Token              string   `json:"token"`
	ConfigFile         string   `json:"config,omitempty"`
	StateFile          string   `json:"state_file,omitempty"`
	IntervalFastS      int      `json:"interval_fast_s"`
	IntervalSlowS      int      `json:"interval_slow_s"`
	FactsMaxIntervalS  int      `json:"facts_max_interval_s"`
	CollectConns       bool     `json:"collect_conns"`
	IncludeMounts      []string `json:"include_mounts,omitempty"`
	ExcludeMounts      []string `json:"exclude_mounts,omitempty"`
	IncludeNICs        []string `json:"include_nics,omitempty"`
	ExcludeNICs        []string `json:"exclude_nics,omitempty"`
	MemIncludeCache    bool     `json:"mem_include_cache"`
	ExecMode           string   `json:"exec_mode"`
	EnableTerminal     bool     `json:"enable_terminal"`
	InsecureSkipVerify bool     `json:"insecure_skip_verify"`
	PreferIPVersion    string   `json:"prefer_ip_version,omitempty"`
}

// DefaultConfig 返回包含默认值的 Config。
func DefaultConfig() *Config {
	return &Config{
		Endpoint:           "",
		Token:              "",
		ConfigFile:         "/etc/dash-agent/config.json",
		StateFile:          "/var/lib/dash-agent/state.json",
		IntervalFastS:      5,
		IntervalSlowS:      60,
		FactsMaxIntervalS:  1800,
		CollectConns:       true,
		IncludeMounts:      nil,
		ExcludeMounts:      nil,
		IncludeNICs:        nil,
		ExcludeNICs:        nil,
		MemIncludeCache:    false,
		ExecMode:           "off",
		EnableTerminal:     false,
		InsecureSkipVerify: false,
		PreferIPVersion:    "",
	}
}

// rawConfigFile 辅助解析可能传入字符串或切片的配置字段。
type rawConfigFile struct {
	Endpoint           *string `json:"endpoint"`
	Token              *string `json:"token"`
	ConfigFile         *string `json:"config"`
	StateFile          *string `json:"state_file"`
	IntervalFastS      *int    `json:"interval_fast_s"`
	IntervalSlowS      *int    `json:"interval_slow_s"`
	FactsMaxIntervalS  *int    `json:"facts_max_interval_s"`
	CollectConns       *bool   `json:"collect_conns"`
	IncludeMounts      any     `json:"include_mounts"`
	ExcludeMounts      any     `json:"exclude_mounts"`
	IncludeNICs        any     `json:"include_nics"`
	ExcludeNICs        any     `json:"exclude_nics"`
	MemIncludeCache    *bool   `json:"mem_include_cache"`
	ExecMode           *string `json:"exec_mode"`
	EnableTerminal     *bool   `json:"enable_terminal"`
	InsecureSkipVerify *bool   `json:"insecure_skip_verify"`
	PreferIPVersion    *string `json:"prefer_ip_version"`
}

func parseStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []any:
		res := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" {
					res = append(res, trimmed)
				}
			}
		}
		return res
	case []string:
		return val
	case string:
		parts := strings.Split(val, ",")
		var res []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				res = append(res, trimmed)
			}
		}
		return res
	default:
		return nil
	}
}

// LoadConfig 按照优先级合成最终配置：
// 命令行参数 (flags) > 环境变量 (DASH_AGENT_*) > 配置文件 > 默认值
func LoadConfig(flagOverrides *Config, setFlags map[string]bool) (*Config, error) {
	cfg := DefaultConfig()

	// 1. 确定配置文件路径（flags > env > default）
	configPath := cfg.ConfigFile
	if envCfg := os.Getenv("DASH_AGENT_CONFIG"); envCfg != "" {
		configPath = envCfg
	}
	if setFlags != nil && setFlags["config"] && flagOverrides != nil && flagOverrides.ConfigFile != "" {
		configPath = flagOverrides.ConfigFile
	}

	// 2. 加载配置文件（如果存在）
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			var raw rawConfigFile
			if err := json.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("parse config file %s: %w", configPath, err)
			}
			applyFileConfig(cfg, &raw)
		} else if setFlags != nil && setFlags["config"] {
			// 如果是命令行显式指定的配置文件但不存在，报错
			return nil, fmt.Errorf("read config file %s: %w", configPath, err)
		}
	}
	cfg.ConfigFile = configPath

	// 3. 环境变量覆盖
	applyEnvOverrides(cfg)

	// 4. 命令行参数覆盖（仅覆盖显式设置的 flag）
	if flagOverrides != nil && setFlags != nil {
		applyFlagOverrides(cfg, flagOverrides, setFlags)
	}

	return cfg, nil
}

func applyFileConfig(cfg *Config, raw *rawConfigFile) {
	if raw.Endpoint != nil {
		cfg.Endpoint = *raw.Endpoint
	}
	if raw.Token != nil {
		cfg.Token = *raw.Token
	}
	if raw.StateFile != nil {
		cfg.StateFile = *raw.StateFile
	}
	if raw.IntervalFastS != nil {
		cfg.IntervalFastS = *raw.IntervalFastS
	}
	if raw.IntervalSlowS != nil {
		cfg.IntervalSlowS = *raw.IntervalSlowS
	}
	if raw.FactsMaxIntervalS != nil {
		cfg.FactsMaxIntervalS = *raw.FactsMaxIntervalS
	}
	if raw.CollectConns != nil {
		cfg.CollectConns = *raw.CollectConns
	}
	if mounts := parseStringSlice(raw.IncludeMounts); mounts != nil {
		cfg.IncludeMounts = mounts
	}
	if mounts := parseStringSlice(raw.ExcludeMounts); mounts != nil {
		cfg.ExcludeMounts = mounts
	}
	if nics := parseStringSlice(raw.IncludeNICs); nics != nil {
		cfg.IncludeNICs = nics
	}
	if nics := parseStringSlice(raw.ExcludeNICs); nics != nil {
		cfg.ExcludeNICs = nics
	}
	if raw.MemIncludeCache != nil {
		cfg.MemIncludeCache = *raw.MemIncludeCache
	}
	if raw.ExecMode != nil {
		cfg.ExecMode = *raw.ExecMode
	}
	if raw.EnableTerminal != nil {
		cfg.EnableTerminal = *raw.EnableTerminal
	}
	if raw.InsecureSkipVerify != nil {
		cfg.InsecureSkipVerify = *raw.InsecureSkipVerify
	}
	if raw.PreferIPVersion != nil {
		cfg.PreferIPVersion = *raw.PreferIPVersion
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("DASH_AGENT_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("DASH_AGENT_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("DASH_AGENT_STATE_FILE"); v != "" {
		cfg.StateFile = v
	}
	if v := os.Getenv("DASH_AGENT_INTERVAL_FAST_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.IntervalFastS = n
		}
	}
	if v := os.Getenv("DASH_AGENT_INTERVAL_SLOW_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.IntervalSlowS = n
		}
	}
	if v := os.Getenv("DASH_AGENT_FACTS_MAX_INTERVAL_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FactsMaxIntervalS = n
		}
	}
	if v := os.Getenv("DASH_AGENT_COLLECT_CONNS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CollectConns = b
		}
	}
	if v := os.Getenv("DASH_AGENT_INCLUDE_MOUNTS"); v != "" {
		cfg.IncludeMounts = parseStringSlice(v)
	}
	if v := os.Getenv("DASH_AGENT_EXCLUDE_MOUNTS"); v != "" {
		cfg.ExcludeMounts = parseStringSlice(v)
	}
	if v := os.Getenv("DASH_AGENT_INCLUDE_NICS"); v != "" {
		cfg.IncludeNICs = parseStringSlice(v)
	}
	if v := os.Getenv("DASH_AGENT_EXCLUDE_NICS"); v != "" {
		cfg.ExcludeNICs = parseStringSlice(v)
	}
	if v := os.Getenv("DASH_AGENT_MEM_INCLUDE_CACHE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.MemIncludeCache = b
		}
	}
	if v := os.Getenv("DASH_AGENT_EXEC_MODE"); v != "" {
		cfg.ExecMode = v
	}
	if v := os.Getenv("DASH_AGENT_ENABLE_TERMINAL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.EnableTerminal = b
		}
	}
	if v := os.Getenv("DASH_AGENT_INSECURE_SKIP_VERIFY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.InsecureSkipVerify = b
		}
	}
	if v := os.Getenv("DASH_AGENT_PREFER_IP_VERSION"); v != "" {
		cfg.PreferIPVersion = v
	}
}

func applyFlagOverrides(cfg *Config, flagOverrides *Config, setFlags map[string]bool) {
	if setFlags["endpoint"] {
		cfg.Endpoint = flagOverrides.Endpoint
	}
	if setFlags["token"] {
		cfg.Token = flagOverrides.Token
	}
	if setFlags["state-file"] {
		cfg.StateFile = flagOverrides.StateFile
	}
	if setFlags["interval-fast-s"] || setFlags["interval-fast"] {
		cfg.IntervalFastS = flagOverrides.IntervalFastS
	}
	if setFlags["interval-slow-s"] || setFlags["interval-slow"] {
		cfg.IntervalSlowS = flagOverrides.IntervalSlowS
	}
	if setFlags["facts-max-interval-s"] || setFlags["facts-max-interval"] {
		cfg.FactsMaxIntervalS = flagOverrides.FactsMaxIntervalS
	}
	if setFlags["collect-conns"] {
		cfg.CollectConns = flagOverrides.CollectConns
	}
	if setFlags["include-mounts"] {
		cfg.IncludeMounts = flagOverrides.IncludeMounts
	}
	if setFlags["exclude-mounts"] {
		cfg.ExcludeMounts = flagOverrides.ExcludeMounts
	}
	if setFlags["include-nics"] {
		cfg.IncludeNICs = flagOverrides.IncludeNICs
	}
	if setFlags["exclude-nics"] {
		cfg.ExcludeNICs = flagOverrides.ExcludeNICs
	}
	if setFlags["mem-include-cache"] {
		cfg.MemIncludeCache = flagOverrides.MemIncludeCache
	}
	if setFlags["exec-mode"] {
		cfg.ExecMode = flagOverrides.ExecMode
	}
	if setFlags["enable-terminal"] {
		cfg.EnableTerminal = flagOverrides.EnableTerminal
	}
	if setFlags["insecure-skip-verify"] {
		cfg.InsecureSkipVerify = flagOverrides.InsecureSkipVerify
	}
	if setFlags["prefer-ip-version"] {
		cfg.PreferIPVersion = flagOverrides.PreferIPVersion
	}
}
