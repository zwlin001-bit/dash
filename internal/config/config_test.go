package config

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dash/internal/logx"
)

func TestConfigFileNotFound_ReadableErrorNotPanic(t *testing.T) {
	nonExistentPath := filepath.Join(t.TempDir(), "non_existent_config.toml")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Load panicked unexpectedly: %v", r)
		}
	}()

	cfg, err := Load(nonExistentPath)
	if err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on error, got %+v", cfg)
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected error wrapping os.ErrNotExist, got: %v", err)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, nonExistentPath) || !strings.Contains(errMsg, "config file not found") {
		t.Errorf("error message should be clear and readable with file path, got: %s", errMsg)
	}
}

func TestConfigValidate(t *testing.T) {
	valid := DefaultConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
	}

	// 1. 无效 driver
	c := DefaultConfig()
	c.DB.Driver = "postgres"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported database driver") {
		t.Errorf("expected error for unsupported driver, got: %v", err)
	}

	// 2. 无效 MaxOpenConns <= 0
	c = DefaultConfig()
	c.DB.MaxOpenConns = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "max_open_conns") {
		t.Errorf("expected error for invalid MaxOpenConns, got: %v", err)
	}

	// 3. 无效 MaxIdleConns < 0
	c = DefaultConfig()
	c.DB.MaxIdleConns = -1
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "max_idle_conns") {
		t.Errorf("expected error for invalid MaxIdleConns, got: %v", err)
	}

	// 4. 空 listen
	c = DefaultConfig()
	c.Server.Listen = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("expected error for empty listen, got: %v", err)
	}

	// 5. 空 data_dir
	c = DefaultConfig()
	c.Server.DataDir = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "data_dir") {
		t.Errorf("expected error for empty data_dir, got: %v", err)
	}
}

func TestConfigRedacted(t *testing.T) {
	originalPass := "SuperSecretDBPassword"
	originalDSN := "root:dsnSecret123@tcp(127.0.0.1:3306)/dash"

	cfg := DefaultConfig()
	cfg.DB.Password = originalPass
	cfg.DB.DSN = originalDSN

	redacted := cfg.Redacted()

	// 1. 验证脱敏副本
	if redacted.DB.Password != "***" {
		t.Errorf("expected redacted password '***', got %q", redacted.DB.Password)
	}
	if !strings.Contains(redacted.DB.DSN, "***") || strings.Contains(redacted.DB.DSN, "dsnSecret123") {
		t.Errorf("expected redacted DSN with masked password, got %q", redacted.DB.DSN)
	}

	// 2. 验证深拷贝未修改原始结构体
	if cfg.DB.Password != originalPass {
		t.Errorf("original password should not be modified, got %q", cfg.DB.Password)
	}
	if cfg.DB.DSN != originalDSN {
		t.Errorf("original DSN should not be modified, got %q", cfg.DB.DSN)
	}
}

func TestConfigPriority_File_Env_CLI(t *testing.T) {
	// 创建基础配置文件
	tomlContent := `
[db]
driver = "oracle"
user = "file_user"
password = "file_password"
dsn = "file_dsn"
wallet_path = "/file/wallet"
max_open_conns = 15
max_idle_conns = 5
conn_max_lifetime_s = 600

[server]
listen = ":8080"
listen_acme = ":8081"
data_dir = "/file/data"
master_key = "/file/master.key"
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(confPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	// 1. 仅有配置文件：应该完全按配置文件加载
	cfgFileOnly, err := Load(confPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}
	if cfgFileOnly.DB.User != "file_user" || cfgFileOnly.DB.Password != "file_password" {
		t.Errorf("file values mismatch: %+v", cfgFileOnly.DB)
	}
	if cfgFileOnly.Server.Listen != ":8080" || cfgFileOnly.DB.MaxOpenConns != 15 {
		t.Errorf("file values mismatch: server listen %s, max_conns %d", cfgFileOnly.Server.Listen, cfgFileOnly.DB.MaxOpenConns)
	}

	// 2. 环境变量覆盖：环境变量应覆盖配置文件
	envMap := map[string]string{
		"DASH_DB_USER":           "env_user",
		"DASH_DB_PASSWORD":       "env_password",
		"DASH_SERVER_LISTEN":     ":9090",
		"DASH_DB_MAX_OPEN_CONNS": "30",
	}
	fakeEnvLookup := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}

	cfgEnv, err := LoadWithOptions(Options{
		ConfigFile: confPath,
		EnvLookup:  fakeEnvLookup,
	})
	if err != nil {
		t.Fatalf("unexpected error loading with env: %v", err)
	}
	// 覆盖项变为环境变量值
	if cfgEnv.DB.User != "env_user" {
		t.Errorf("expected DB.User to be overridden by env, got %s", cfgEnv.DB.User)
	}
	if cfgEnv.DB.Password != "env_password" {
		t.Errorf("expected DB.Password to be overridden by env, got %s", cfgEnv.DB.Password)
	}
	if cfgEnv.Server.Listen != ":9090" {
		t.Errorf("expected Server.Listen to be overridden by env, got %s", cfgEnv.Server.Listen)
	}
	if cfgEnv.DB.MaxOpenConns != 30 {
		t.Errorf("expected DB.MaxOpenConns to be overridden by env, got %d", cfgEnv.DB.MaxOpenConns)
	}
	// 未被环境变量覆盖的项仍保留配置文件值
	if cfgEnv.DB.DSN != "file_dsn" || cfgEnv.Server.DataDir != "/file/data" {
		t.Errorf("uncovered fields should remain file values, got DSN=%s, DataDir=%s", cfgEnv.DB.DSN, cfgEnv.Server.DataDir)
	}

	// 3. 命令行覆盖：命令行应覆盖环境变量和配置文件
	maxConnsCLI := 50
	cfgCLI, err := LoadWithOptions(Options{
		ConfigFile:     confPath,
		EnvLookup:      fakeEnvLookup,
		DBUser:         "cli_user",
		DBPassword:     "cli_password",
		ServerListen:   ":443",
		DBMaxOpenConns: &maxConnsCLI,
	})
	if err != nil {
		t.Fatalf("unexpected error loading with CLI: %v", err)
	}

	// 命令行项胜出
	if cfgCLI.DB.User != "cli_user" {
		t.Errorf("CLI DB.User expected 'cli_user', got %s", cfgCLI.DB.User)
	}
	if cfgCLI.DB.Password != "cli_password" {
		t.Errorf("CLI DB.Password expected 'cli_password', got %s", cfgCLI.DB.Password)
	}
	if cfgCLI.Server.Listen != ":443" {
		t.Errorf("CLI Server.Listen expected ':443', got %s", cfgCLI.Server.Listen)
	}
	if cfgCLI.DB.MaxOpenConns != 50 {
		t.Errorf("CLI DB.MaxOpenConns expected 50, got %d", cfgCLI.DB.MaxOpenConns)
	}
	// 命令行未指定的，继承自配置文件
	if cfgCLI.DB.MaxIdleConns != 5 {
		t.Errorf("DB.MaxIdleConns should remain file value 5, got %d", cfgCLI.DB.MaxIdleConns)
	}
}

func TestRegisterFlags_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(confPath, []byte(`[db]`+"\n"+`driver = "oracle"`), 0600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	fs := flag.NewFlagSet("dashd-test", flag.ContinueOnError)
	cliFlags := RegisterFlags(fs)

	args := []string{
		"-config", confPath,
		"-db-user", "flag_user",
		"-db-password", "flag_password",
		"-server-listen", ":8443",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("flag parse failed: %v", err)
	}

	opts := cliFlags.Options()
	cfg, err := LoadWithOptions(opts)
	if err != nil {
		t.Fatalf("LoadWithOptions failed: %v", err)
	}

	if cfg.DB.User != "flag_user" {
		t.Errorf("expected DB.User = 'flag_user', got %s", cfg.DB.User)
	}
	if cfg.DB.Password != "flag_password" {
		t.Errorf("expected DB.Password = 'flag_password', got %s", cfg.DB.Password)
	}
	if cfg.Server.Listen != ":8443" {
		t.Errorf("expected Server.Listen = ':8443', got %s", cfg.Server.Listen)
	}
	// 默认值未被破坏
	if cfg.DB.MaxOpenConns != 20 {
		t.Errorf("expected default MaxOpenConns = 20, got %d", cfg.DB.MaxOpenConns)
	}
}

func TestEnvVarExpansionInConfigFile(t *testing.T) {
	tomlContent := `
[db]
driver = "oracle"
user = "admin"
password = "${DASH_DB_PASSWORD}"
dsn = "user:secret@tcp(127.0.0.1:3306)/dash"
wallet_path = "${WALLET_DIR}/wallet"
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(confPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	fakeEnv := map[string]string{
		"DASH_DB_PASSWORD": "SecretFromEnvironment#123",
		"WALLET_DIR":       "/opt/secrets",
	}

	cfg, err := LoadWithOptions(Options{
		ConfigFile: confPath,
		EnvLookup: func(k string) (string, bool) {
			v, ok := fakeEnv[k]
			return v, ok
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DB.Password != "SecretFromEnvironment#123" {
		t.Errorf("expected password to expand to 'SecretFromEnvironment#123', got %q", cfg.DB.Password)
	}
	if cfg.DB.WalletPath != "/opt/secrets/wallet" {
		t.Errorf("expected wallet_path to expand to '/opt/secrets/wallet', got %q", cfg.DB.WalletPath)
	}
}

func TestEnvVarExpansion_PreservesDollarAndUndefined(t *testing.T) {
	// F2: os.Expand 会连 $VAR（无花括号）一起展开。DSN 里若含 $ 会被误吞。
	// 改成只展开 ${VAR} 形式，或对未定义变量保留原样
	tomlContent := `
[db]
driver = "oracle"
user = "admin"
password = "${MY_DB_PASS}"
dsn = "root:p$ssword123$1@tcp(127.0.0.1:3306)/dash?$flag=1"
wallet_path = "${UNDEFINED_WALLET}/wallet"
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(confPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	fakeEnv := map[string]string{
		"MY_DB_PASS": "actual_secret_pass",
		// 注意：未定义 UNDEFINED_WALLET
	}

	cfg, err := LoadWithOptions(Options{
		ConfigFile: confPath,
		EnvLookup: func(k string) (string, bool) {
			v, ok := fakeEnv[k]
			return v, ok
		},
	})
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// 1. ${MY_DB_PASS} 成功展开
	if cfg.DB.Password != "actual_secret_pass" {
		t.Errorf("expected password to expand to 'actual_secret_pass', got %q", cfg.DB.Password)
	}

	// 2. DSN 中的 $ssword123, $1, $flag（无花括号）不被误吞
	expectedDSN := "root:p$ssword123$1@tcp(127.0.0.1:3306)/dash?$flag=1"
	if cfg.DB.DSN != expectedDSN {
		t.Errorf("DSN dollar signs were modified!\ngot:  %q\nwant: %q", cfg.DB.DSN, expectedDSN)
	}

	// 3. 未定义环境变量保留原样 ${UNDEFINED_WALLET}
	expectedWallet := "${UNDEFINED_WALLET}/wallet"
	if cfg.DB.WalletPath != expectedWallet {
		t.Errorf("undefined env var placeholder was not preserved!\ngot:  %q\nwant: %q", cfg.DB.WalletPath, expectedWallet)
	}
}

func TestDeployExampleConfigFile_ParsesSuccessfully(t *testing.T) {
	examplePath := filepath.Join("..", "..", "deploy", "config.example.toml")
	if _, err := os.Stat(examplePath); err != nil {
		examplePath = "/opt/git/dash/deploy/config.example.toml"
	}

	fakeEnv := map[string]string{
		"DASH_DB_PASSWORD": "example_env_password",
	}
	cfg, err := LoadWithOptions(Options{
		ConfigFile: examplePath,
		EnvLookup: func(k string) (string, bool) {
			v, ok := fakeEnv[k]
			return v, ok
		},
	})
	if err != nil {
		t.Fatalf("deploy/config.example.toml failed to parse: %v", err)
	}

	if cfg.DB.Driver != "oracle" {
		t.Errorf("expected db.driver = oracle, got %s", cfg.DB.Driver)
	}
	if cfg.DB.Password != "example_env_password" {
		t.Errorf("expected db.password expanded from env, got %s", cfg.DB.Password)
	}
	if cfg.DB.MaxOpenConns != 20 {
		t.Errorf("expected max_open_conns = 20, got %d", cfg.DB.MaxOpenConns)
	}
	if cfg.Server.Listen != ":443" {
		t.Errorf("expected server.listen = :443, got %s", cfg.Server.Listen)
	}
}

func TestConfigLog_RedactionEndToEnd(t *testing.T) {
	// 完整测试验收项 2 & 3：
	// 把一个带密码的完整配置打进日志，输出里翻不到明文密码（grep 日志）
	// DSN 里内嵌密码的情况也要被打码
	secretPassword := "SuperSecretPlaintextPassword_98765"
	embeddedDsnSecret := "EmbeddedDsnPassword_54321"

	tomlContent := `
[db]
driver = "oracle"
user = "admin"
password = "` + secretPassword + `"
dsn = "admin:` + embeddedDsnSecret + `@tcp(adb.oraclecloud.com:1522)/tp"
wallet_path = "/etc/dash/wallet"

[server]
listen = ":443"
data_dir = "/var/lib/dash"
master_key = "/etc/dash/master.key"
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(confPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(confPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// 捕获日志输出
	var logBuf bytes.Buffer
	logger := logx.New(&logBuf, logx.LevelInfo)

	// 使用 cfg.Redacted() 打印
	redactedCfg := cfg.Redacted()
	logger.Info("application bootstrap configuration", "config", logx.RedactConfig(redactedCfg))

	logOutput := logBuf.String()

	// 模拟 grep 日志验证
	if strings.Contains(logOutput, secretPassword) {
		t.Fatalf("CRITICAL SECURITY FAILURE: Log output leaked plain DB password %q! Log content:\n%s", secretPassword, logOutput)
	}
	if strings.Contains(logOutput, embeddedDsnSecret) {
		t.Fatalf("CRITICAL SECURITY FAILURE: Log output leaked embedded DSN password %q! Log content:\n%s", embeddedDsnSecret, logOutput)
	}

	// 验证打码字符存在
	if !strings.Contains(logOutput, "***") {
		t.Fatalf("Expected '***' in redacted log output, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "admin:***@tcp(adb.oraclecloud.com:1522)/tp") {
		t.Fatalf("Expected DSN password masked to ***, got:\n%s", logOutput)
	}
}

func TestDevNoAuth_ConfigAndEnv(t *testing.T) {
	// 1. 默认值必须为 false
	defCfg := DefaultConfig()
	if defCfg.Server.DevNoAuth {
		t.Fatalf("expected DefaultConfig DevNoAuth to be false, got true")
	}

	// 2. 配置文件不写时，默认为 false
	tomlWithout := `
[db]
driver = "mysql"
dsn = "user:pass@tcp(127.0.0.1:3306)/dash"
[server]
listen = ":8080"
data_dir = "/tmp/dash"
`
	tmpDir := t.TempDir()
	confPath1 := filepath.Join(tmpDir, "config1.toml")
	if err := os.WriteFile(confPath1, []byte(tomlWithout), 0600); err != nil {
		t.Fatalf("failed to write config1: %v", err)
	}
	cfg1, err := Load(confPath1)
	if err != nil {
		t.Fatalf("failed to load config1: %v", err)
	}
	if cfg1.Server.DevNoAuth {
		t.Fatalf("expected DevNoAuth to be false when omitted, got true")
	}

	// 3. 配置文件显式写 dev_no_auth = true
	tomlWith := `
[db]
driver = "mysql"
dsn = "user:pass@tcp(127.0.0.1:3306)/dash"
[server]
listen = ":8080"
data_dir = "/tmp/dash"
dev_no_auth = true
`
	confPath2 := filepath.Join(tmpDir, "config2.toml")
	if err := os.WriteFile(confPath2, []byte(tomlWith), 0600); err != nil {
		t.Fatalf("failed to write config2: %v", err)
	}
	cfg2, err := Load(confPath2)
	if err != nil {
		t.Fatalf("failed to load config2: %v", err)
	}
	if !cfg2.Server.DevNoAuth {
		t.Fatalf("expected DevNoAuth to be true from config file, got false")
	}

	// 4. 环境变量覆盖 DASH_SERVER_DEV_NO_AUTH
	envMap := map[string]string{
		"DASH_SERVER_DEV_NO_AUTH": "true",
	}
	mockLookup := func(key string) (string, bool) {
		val, ok := envMap[key]
		return val, ok
	}
	cfg3, err := LoadWithOptions(Options{
		ConfigFile: confPath1,
		EnvLookup:  mockLookup,
	})
	if err != nil {
		t.Fatalf("failed to load with DASH_SERVER_DEV_NO_AUTH: %v", err)
	}
	if !cfg3.Server.DevNoAuth {
		t.Fatalf("expected DevNoAuth to be true from DASH_SERVER_DEV_NO_AUTH, got false")
	}

	// 5. 环境变量覆盖 DEV_NO_AUTH
	envMap2 := map[string]string{
		"DEV_NO_AUTH": "1",
	}
	mockLookup2 := func(key string) (string, bool) {
		val, ok := envMap2[key]
		return val, ok
	}
	cfg4, err := LoadWithOptions(Options{
		ConfigFile: confPath1,
		EnvLookup:  mockLookup2,
	})
	if err != nil {
		t.Fatalf("failed to load with DEV_NO_AUTH: %v", err)
	}
	if !cfg4.Server.DevNoAuth {
		t.Fatalf("expected DevNoAuth to be true from DEV_NO_AUTH, got false")
	}
}

