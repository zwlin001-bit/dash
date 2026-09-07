package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"dash/internal/db"
)

// ServerConfig defines basic bootstrap server options.
type ServerConfig struct {
	Listen     string `toml:"listen"`
	ListenACME string `toml:"listen_acme"`
	DataDir    string `toml:"data_dir"`
	MasterKey  string `toml:"master_key"`
}

// Config wraps DB and Server bootstrap configurations.
type Config struct {
	DB     db.Config    `toml:"db"`
	Server ServerConfig `toml:"server"`
}

// Load loads configuration from the given file path, falling back to environment variables.
func Load(path string) (*Config, error) {
	cfg := &Config{
		DB: db.Config{
			Driver:           "oracle",
			MaxOpenConns:     20,
			MaxIdleConns:     10,
			ConnMaxLifetimeS: 1800,
		},
		Server: ServerConfig{
			Listen:     ":443",
			ListenACME: ":80",
			DataDir:    "/var/lib/dash",
			MasterKey:  "/etc/dash/master.key",
		},
	}

	// Try reading file if provided and exists
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: failed to read file %q: %w", path, err)
			}
		} else {
			// Expand env variables in config content
			expanded := os.ExpandEnv(string(data))
			if _, err := toml.Decode(expanded, cfg); err != nil {
				return nil, fmt.Errorf("config: failed to parse TOML %q: %w", path, err)
			}
		}
	}

	// Try fallback to /opt/tool/dash/config/dash.conf & dash.local.conf if DB DSN is empty
	if cfg.DB.DSN == "" && path == "/etc/dash/config.toml" {
		loadLegacyConfFallback(cfg)
	}

	// Override with DASH_* environment variables
	applyEnvOverrides(cfg)

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if val := os.Getenv("DASH_DB_DRIVER"); val != "" {
		cfg.DB.Driver = val
	}
	if val := os.Getenv("DASH_DB_USER"); val != "" {
		cfg.DB.User = val
	}
	if val := os.Getenv("DASH_DB_PASSWORD"); val != "" {
		cfg.DB.Password = val
	}
	if val := os.Getenv("DASH_DB_DSN"); val != "" {
		cfg.DB.DSN = val
	}
	if val := os.Getenv("DASH_DB_WALLET_PATH"); val != "" {
		cfg.DB.WalletPath = val
	}
	if val := os.Getenv("DASH_DB_MAX_OPEN_CONNS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.DB.MaxOpenConns = n
		}
	}
	if val := os.Getenv("DASH_DB_MAX_IDLE_CONNS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.DB.MaxIdleConns = n
		}
	}
	if val := os.Getenv("DASH_SERVER_LISTEN"); val != "" {
		cfg.Server.Listen = val
	}
	if val := os.Getenv("DASH_SERVER_DATA_DIR"); val != "" {
		cfg.Server.DataDir = val
	}
	if val := os.Getenv("DASH_SERVER_MASTER_KEY"); val != "" {
		cfg.Server.MasterKey = val
	}
}

func loadLegacyConfFallback(cfg *Config) {
	parseFile := func(filepath string) map[string]string {
		res := make(map[string]string)
		data, err := os.ReadFile(filepath)
		if err != nil {
			return res
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				v := strings.TrimSpace(parts[1])
				v = strings.Trim(v, `"'`)
				res[k] = v
			}
		}
		return res
	}

	conf := parseFile("/opt/tool/dash/config/dash.conf")
	localConf := parseFile("/opt/tool/dash/config/dash.local.conf")

	if dsn, ok := conf["DB_DSN"]; ok && dsn != "" {
		cfg.DB.DSN = dsn
		cfg.DB.Driver = "oracle"
	}
	if user, ok := localConf["DB_USER"]; ok && user != "" {
		cfg.DB.User = user
	}
	if pass, ok := localConf["DB_PASSWORD"]; ok && pass != "" {
		cfg.DB.Password = pass
	}
}
