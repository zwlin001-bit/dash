package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"dash/internal/db"
	"dash/internal/logx"
)

// DefaultConfigPath 默认配置文件绝对路径
const DefaultConfigPath = "/etc/dash/config.toml"

// DBConfig 数据库自举配置
type DBConfig struct {
	Driver           string `toml:"driver" json:"driver"`                           // "oracle" | "mysql"
	User             string `toml:"user" json:"user"`                               // 数据库用户名
	Password         string `toml:"password" json:"password" redact:"true"`         // 支持 ${DASH_DB_PASSWORD} 占位
	DSN              string `toml:"dsn" json:"dsn"`                                 // 连接串
	WalletPath       string `toml:"wallet_path" json:"wallet_path"`                 // ADB wallet 目录
	MaxOpenConns     int    `toml:"max_open_conns" json:"max_open_conns"`           // 默认 20
	MaxIdleConns     int    `toml:"max_idle_conns" json:"max_idle_conns"`           // 默认 10
	ConnMaxLifetimeS int    `toml:"conn_max_lifetime_s" json:"conn_max_lifetime_s"` // 默认 1800
}

// ServerConfig 服务端运行时自举配置（不含域名等业务配置）
type ServerConfig struct {
	Listen     string `toml:"listen" json:"listen"`           // 默认 ":443"
	ListenACME string `toml:"listen_acme" json:"listen_acme"` // 默认 ":80"
	DataDir    string `toml:"data_dir" json:"data_dir"`       // 默认 "/var/lib/dash"
	MasterKey  string `toml:"master_key" json:"master_key"`   // 默认 "/etc/dash/master.key"
	DevNoAuth  bool   `toml:"dev_no_auth" json:"dev_no_auth"` // 默认 false，★ 仅供开发调试
}

// Config 服务端启动自举配置根结构
type Config struct {
	DB     DBConfig     `toml:"db" json:"db"`
	Server ServerConfig `toml:"server" json:"server"`
}

// DefaultConfig 返回预设了合理默认值的 Config 实例
func DefaultConfig() *Config {
	return &Config{
		DB: DBConfig{
			Driver:           "oracle",
			User:             "admin",
			Password:         "",
			DSN:              "",
			WalletPath:       "",
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
}

// Validate 校验配置合法性
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}

	driver := strings.ToLower(strings.TrimSpace(c.DB.Driver))
	if driver != "oracle" && driver != "mysql" {
		return fmt.Errorf("unsupported database driver: %q (must be 'oracle' or 'mysql')", c.DB.Driver)
	}

	if c.DB.MaxOpenConns <= 0 {
		return fmt.Errorf("invalid db.max_open_conns: %d (must be > 0)", c.DB.MaxOpenConns)
	}
	if c.DB.MaxIdleConns < 0 {
		return fmt.Errorf("invalid db.max_idle_conns: %d (must be >= 0)", c.DB.MaxIdleConns)
	}
	if c.DB.ConnMaxLifetimeS < 0 {
		return fmt.Errorf("invalid db.conn_max_lifetime_s: %d (must be >= 0)", c.DB.ConnMaxLifetimeS)
	}

	if strings.TrimSpace(c.Server.Listen) == "" {
		return errors.New("server.listen cannot be empty")
	}
	if strings.TrimSpace(c.Server.DataDir) == "" {
		return errors.New("server.data_dir cannot be empty")
	}

	return nil
}

// Redacted 深拷贝并把机密替换成 "***"，用于日志安全打印
func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}

	cp := *c
	if cp.DB.Password != "" {
		cp.DB.Password = "***"
	}
	if cp.DB.DSN != "" {
		cp.DB.DSN = logx.RedactDSN(cp.DB.DSN)
	}

	return &cp
}

// Options 控制配置加载的行为及覆盖项（如命令行参数）
type Options struct {
	ConfigFile         string
	DBDriver           string
	DBUser             string
	DBPassword         string
	DBDSN              string
	DBWalletPath       string
	DBMaxOpenConns     *int
	DBMaxIdleConns     *int
	DBConnMaxLifetimeS *int
	ServerListen       string
	ServerListenACME   string
	ServerDataDir      string
	ServerMasterKey    string
	ServerDevNoAuth    *bool

	// EnvLookup 允许测试注入自定义环境变量查找函数，默认为 os.LookupEnv
	EnvLookup func(key string) (string, bool)
}

// CLIFlags 包装通过 flag.FlagSet 注册的命令行参数
type CLIFlags struct {
	fs                 *flag.FlagSet
	configFile         *string
	dbDriver           *string
	dbUser             *string
	dbPassword         *string
	dbDSN              *string
	dbWalletPath       *string
	dbMaxOpenConns     *int
	dbMaxIdleConns     *int
	dbConnMaxLifetimeS *int
	serverListen       *string
	serverListenACME   *string
	serverDataDir      *string
	serverMasterKey    *string
}

// RegisterFlags 向指定 FlagSet 注册命令行配置参数
func RegisterFlags(fs *flag.FlagSet) *CLIFlags {
	cf := &CLIFlags{fs: fs}
	cf.configFile = fs.String("config", "", "path to config file (default /etc/dash/config.toml)")
	cf.dbDriver = fs.String("db-driver", "", "database driver (oracle|mysql)")
	cf.dbUser = fs.String("db-user", "", "database user")
	cf.dbPassword = fs.String("db-password", "", "database password")
	cf.dbDSN = fs.String("db-dsn", "", "database DSN/connection string")
	cf.dbWalletPath = fs.String("db-wallet-path", "", "Oracle ADB wallet directory")
	cf.dbMaxOpenConns = fs.Int("db-max-open-conns", 0, "database connection pool max open conns")
	cf.dbMaxIdleConns = fs.Int("db-max-idle-conns", 0, "database connection pool max idle conns")
	cf.dbConnMaxLifetimeS = fs.Int("db-conn-max-lifetime-s", 0, "database connection pool max lifetime in seconds")
	cf.serverListen = fs.String("server-listen", "", "server listen address (e.g. :443)")
	cf.serverListenACME = fs.String("server-listen-acme", "", "ACME HTTP-01 challenge listen address (e.g. :80)")
	cf.serverDataDir = fs.String("server-data-dir", "", "server runtime data directory")
	cf.serverMasterKey = fs.String("server-master-key", "", "credentials master key file path")
	return cf
}

// Options 返回仅包含命令行显式指定参数的 Options
func (cf *CLIFlags) Options() Options {
	opts := Options{}
	if cf.fs == nil {
		return opts
	}

	cf.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config":
			opts.ConfigFile = *cf.configFile
		case "db-driver":
			opts.DBDriver = *cf.dbDriver
		case "db-user":
			opts.DBUser = *cf.dbUser
		case "db-password":
			opts.DBPassword = *cf.dbPassword
		case "db-dsn":
			opts.DBDSN = *cf.dbDSN
		case "db-wallet-path":
			opts.DBWalletPath = *cf.dbWalletPath
		case "db-max-open-conns":
			val := *cf.dbMaxOpenConns
			opts.DBMaxOpenConns = &val
		case "db-max-idle-conns":
			val := *cf.dbMaxIdleConns
			opts.DBMaxIdleConns = &val
		case "db-conn-max-lifetime-s":
			val := *cf.dbConnMaxLifetimeS
			opts.DBConnMaxLifetimeS = &val
		case "server-listen":
			opts.ServerListen = *cf.serverListen
		case "server-listen-acme":
			opts.ServerListenACME = *cf.serverListenACME
		case "server-data-dir":
			opts.ServerDataDir = *cf.serverDataDir
		case "server-master-key":
			opts.ServerMasterKey = *cf.serverMasterKey
		}
	})
	return opts
}

// Load 读取并解析配置文件：文件 → 环境变量覆盖 → 校验 → 填默认值。
// 若 path 为空，则优先检查环境变量 DASH_CONFIG，若亦未设置则使用 DefaultConfigPath。
func Load(path string) (*Config, error) {
	return LoadWithOptions(Options{ConfigFile: path})
}

var envVarPlaceholderRegex = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// expandEnvPlaceholders 仅展开 ${VAR} 形式的环境变量占位符。
// 不带花括号的 $VAR 保持原样（避免 DSN 中包含 $ 字符时被误吞）；
// 未定义的环境变量同样保留原样 ${VAR}。
func expandEnvPlaceholders(content string, lookup func(string) (string, bool)) string {
	return envVarPlaceholderRegex.ReplaceAllStringFunc(content, func(match string) string {
		varName := match[2 : len(match)-1]
		if val, ok := lookup(varName); ok {
			return val
		}
		return match
	})
}

// LoadWithOptions 按「命令行 > 环境变量 > 配置文件 > 默认值」优先级解析完整配置并校验。
func LoadWithOptions(opts Options) (*Config, error) {
	lookup := opts.EnvLookup
	if lookup == nil {
		lookup = os.LookupEnv
	}

	// 1. 确定配置文件路径：命令行 > 环境变量 > 默认路径
	configPath := opts.ConfigFile
	if configPath == "" {
		if envPath, ok := lookup("DASH_CONFIG"); ok && strings.TrimSpace(envPath) != "" {
			configPath = strings.TrimSpace(envPath)
		} else {
			configPath = DefaultConfigPath
		}
	}

	// 2. 读取配置文件内容（缺失时返回友好且可识别的错误，绝不 panic）
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s: %w", configPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// 3. 展开配置内容中的 ${VAR} 环境变量占位符
	expanded := expandEnvPlaceholders(string(data), lookup)

	// 4. 解析 TOML 到包含默认值的结构体中
	cfg := DefaultConfig()
	if err := toml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse toml config %s: %w", configPath, err)
	}

	// 5. 环境变量覆盖 (DASH_*)：DASH_ + 大写路径，. 换成 _
	if val, ok := lookup("DASH_DB_DRIVER"); ok && strings.TrimSpace(val) != "" {
		cfg.DB.Driver = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_DB_USER"); ok && strings.TrimSpace(val) != "" {
		cfg.DB.User = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_DB_PASSWORD"); ok {
		cfg.DB.Password = val
	}
	if val, ok := lookup("DASH_DB_DSN"); ok && strings.TrimSpace(val) != "" {
		cfg.DB.DSN = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_DB_WALLET_PATH"); ok {
		cfg.DB.WalletPath = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_DB_MAX_OPEN_CONNS"); ok && strings.TrimSpace(val) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			cfg.DB.MaxOpenConns = n
		}
	}
	if val, ok := lookup("DASH_DB_MAX_IDLE_CONNS"); ok && strings.TrimSpace(val) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			cfg.DB.MaxIdleConns = n
		}
	}
	if val, ok := lookup("DASH_DB_CONN_MAX_LIFETIME_S"); ok && strings.TrimSpace(val) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			cfg.DB.ConnMaxLifetimeS = n
		}
	}
	if val, ok := lookup("DASH_SERVER_LISTEN"); ok && strings.TrimSpace(val) != "" {
		cfg.Server.Listen = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_SERVER_LISTEN_ACME"); ok && strings.TrimSpace(val) != "" {
		cfg.Server.ListenACME = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_SERVER_DATA_DIR"); ok && strings.TrimSpace(val) != "" {
		cfg.Server.DataDir = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_SERVER_MASTER_KEY"); ok && strings.TrimSpace(val) != "" {
		cfg.Server.MasterKey = strings.TrimSpace(val)
	}
	if val, ok := lookup("DASH_SERVER_DEV_NO_AUTH"); ok && strings.TrimSpace(val) != "" {
		v := strings.ToLower(strings.TrimSpace(val))
		cfg.Server.DevNoAuth = (v == "true" || v == "1" || v == "yes" || v == "on")
	} else if val, ok := lookup("DEV_NO_AUTH"); ok && strings.TrimSpace(val) != "" {
		v := strings.ToLower(strings.TrimSpace(val))
		cfg.Server.DevNoAuth = (v == "true" || v == "1" || v == "yes" || v == "on")
	}

	// 6. 命令行覆盖（最高优先级）
	if opts.DBDriver != "" {
		cfg.DB.Driver = opts.DBDriver
	}
	if opts.DBUser != "" {
		cfg.DB.User = opts.DBUser
	}
	if opts.DBPassword != "" {
		cfg.DB.Password = opts.DBPassword
	}
	if opts.DBDSN != "" {
		cfg.DB.DSN = opts.DBDSN
	}
	if opts.DBWalletPath != "" {
		cfg.DB.WalletPath = opts.DBWalletPath
	}
	if opts.DBMaxOpenConns != nil {
		cfg.DB.MaxOpenConns = *opts.DBMaxOpenConns
	}
	if opts.DBMaxIdleConns != nil {
		cfg.DB.MaxIdleConns = *opts.DBMaxIdleConns
	}
	if opts.DBConnMaxLifetimeS != nil {
		cfg.DB.ConnMaxLifetimeS = *opts.DBConnMaxLifetimeS
	}
	if opts.ServerListen != "" {
		cfg.Server.Listen = opts.ServerListen
	}
	if opts.ServerListenACME != "" {
		cfg.Server.ListenACME = opts.ServerListenACME
	}
	if opts.ServerDataDir != "" {
		cfg.Server.DataDir = opts.ServerDataDir
	}
	if opts.ServerMasterKey != "" {
		cfg.Server.MasterKey = opts.ServerMasterKey
	}
	if opts.ServerDevNoAuth != nil {
		cfg.Server.DevNoAuth = *opts.ServerDevNoAuth
	}

	// 7. 规范化并校验配置
	cfg.DB.Driver = strings.ToLower(strings.TrimSpace(cfg.DB.Driver))
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// DBOptions 把自举配置转换成 internal/db 的连接参数。
// 两者字段同构但分属不同层：config 面向配置文件，db.Config 面向连接层。
func (c *DBConfig) DBOptions() db.Config {
	return db.Config{
		Driver:           c.Driver,
		User:             c.User,
		Password:         c.Password,
		DSN:              c.DSN,
		WalletPath:       c.WalletPath,
		MaxOpenConns:     c.MaxOpenConns,
		MaxIdleConns:     c.MaxIdleConns,
		ConnMaxLifetimeS: c.ConnMaxLifetimeS,
	}
}
