package logx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type sampleDBConfig struct {
	Driver           string `toml:"driver" json:"driver"`
	User             string `toml:"user" json:"user"`
	Password         string `toml:"password" json:"password" redact:"true"`
	DSN              string `toml:"dsn" json:"dsn"`
	WalletPath       string `toml:"wallet_path" json:"wallet_path"`
	MaxOpenConns     int    `toml:"max_open_conns" json:"max_open_conns"`
	MaxIdleConns     int    `toml:"max_idle_conns" json:"max_idle_conns"`
	ConnMaxLifetimeS int    `toml:"conn_max_lifetime_s" json:"conn_max_lifetime_s"`
}

type sampleServerConfig struct {
	Listen     string `toml:"listen" json:"listen"`
	ListenACME string `toml:"listen_acme" json:"listen_acme"`
	DataDir    string `toml:"data_dir" json:"data_dir"`
	MasterKey  string `toml:"master_key" json:"master_key"`
}

type sampleConfig struct {
	DB     sampleDBConfig     `toml:"db" json:"db"`
	Server sampleServerConfig `toml:"server" json:"server"`
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "MySQL standard DSN",
			input:    "root:my_secret_pwd@tcp(127.0.0.1:3306)/dash?charset=utf8mb4",
			expected: "root:***@tcp(127.0.0.1:3306)/dash?charset=utf8mb4",
		},
		{
			name:     "MySQL URI DSN",
			input:    "mysql://admin:secret123@192.168.1.100:3306/dash_prod",
			expected: "mysql://admin:***@192.168.1.100:3306/dash_prod",
		},
		{
			name:     "Postgres URI DSN with @ in password",
			input:    "postgres://dash_user:P@ssword_99!@localhost:5432/dash?sslmode=disable",
			expected: "postgres://dash_user:***@localhost:5432/dash?sslmode=disable",
		},
		{
			name:     "Oracle EZConnect format",
			input:    "admin/SuperSecret456@adb.ap-tokyo-1.oraclecloud.com:1522/tp_service",
			expected: "admin/***@adb.ap-tokyo-1.oraclecloud.com:1522/tp_service",
		},
		{
			name:     "Oracle TNS Descriptor with password",
			input:    "(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=adb.oraclecloud.com)(PORT=1522))(CONNECT_DATA=(SERVICE_NAME=tp))(SECURITY=(USER_ID=admin)(PASSWORD=VerySecretAdbPwd123)))",
			expected: "(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=adb.oraclecloud.com)(PORT=1522))(CONNECT_DATA=(SERVICE_NAME=tp))(SECURITY=(USER_ID=admin)(PASSWORD=***)))",
		},
		{
			name:     "Key-value style password in DSN",
			input:    "user=admin password=secret_token_val dbname=dash",
			expected: "user=admin password=*** dbname=dash",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactDSN(tc.input)
			if got != tc.expected {
				t.Errorf("RedactDSN(%q)\ngot:  %q\nwant: %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestRedactToken(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Short token <= 4 chars",
			input:    "abcd",
			expected: "***",
		},
		{
			name:     "Standard token keeps last 4 chars",
			input:    "agent_token_abcdef1234",
			expected: "***1234",
		},
		{
			name:     "Enrollment token in key-value string",
			input:    "enrollment_token=enroll_secret_9988",
			expected: "enrollment_token=***9988",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.input, "=") {
				got := Redact(tc.input)
				if got != tc.expected {
					t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.expected)
				}
			} else {
				got := RedactToken(tc.input)
				if got != tc.expected {
					t.Errorf("RedactToken(%q) = %q, want %q", tc.input, got, tc.expected)
				}
			}
		})
	}
}

func TestRedact_Comprehensive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Quoted password in config line",
			input:    `password = "quoted_secret_123"`,
			expected: `password = "***"`,
		},
		{
			name:     "URL query parameter with secret",
			input:    "https://api.example.com/v1/webhook?api_key=ak_live_xyz987654&verbose=1",
			expected: "https://api.example.com/v1/webhook?api_key=***7654&verbose=1",
		},
		{
			name:     "Bearer token in header",
			input:    "Authorization: Bearer mySecretToken1234",
			expected: "Authorization: Bearer ***1234",
		},
		{
			name:     "Normal string without secret",
			input:    "server listening on :443 with data_dir=/var/lib/dash",
			expected: "server listening on :443 with data_dir=/var/lib/dash",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.expected {
				t.Errorf("Redact(%q)\ngot:  %q\nwant: %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestRedactConfig(t *testing.T) {
	cfg := sampleConfig{
		DB: sampleDBConfig{
			Driver:           "oracle",
			User:             "admin",
			Password:         "MyUltraSecureDBPass#2026",
			DSN:              "admin:EmbeddedDsnPassword999@tcp(adb.oraclecloud.com:1522)/tp",
			WalletPath:       "/etc/dash/wallet",
			MaxOpenConns:     20,
			MaxIdleConns:     10,
			ConnMaxLifetimeS: 1800,
		},
		Server: sampleServerConfig{
			Listen:     ":443",
			ListenACME: ":80",
			DataDir:    "/var/lib/dash",
			MasterKey:  "/etc/dash/master.key",
		},
	}

	redactedStr := RedactConfig(cfg)

	// 1. 绝对不能出现明文密码
	if strings.Contains(redactedStr, "MyUltraSecureDBPass#2026") {
		t.Fatalf("redacted config leaked plain DB password: %s", redactedStr)
	}
	if strings.Contains(redactedStr, "EmbeddedDsnPassword999") {
		t.Fatalf("redacted config leaked embedded DSN password: %s", redactedStr)
	}

	// 2. 应该包含 ***
	if !strings.Contains(redactedStr, "***") {
		t.Fatalf("redacted config does not contain ***: %s", redactedStr)
	}

	// 3. 必须是合法有效的 JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(redactedStr), &parsed); err != nil {
		t.Fatalf("redacted config is not valid JSON: %v, content: %s", err, redactedStr)
	}

	// 4. 验证具体字段被安全打码
	dbMap, ok := parsed["db"].(map[string]any)
	if !ok {
		t.Fatalf("expected db section in parsed JSON: %v", parsed)
	}
	if dbMap["password"] != "***" {
		t.Errorf("expected db.password to be '***', got %v", dbMap["password"])
	}
	if !strings.Contains(dbMap["dsn"].(string), "***") {
		t.Errorf("expected db.dsn to contain '***', got %v", dbMap["dsn"])
	}
}

func TestLoggerOutput_NoPlainTextSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, LevelDebug, "json")

	dbPass := "TopSecretPassword777"
	dsnPass := "DsnInternalSecret888"
	cfg := sampleConfig{
		DB: sampleDBConfig{
			Driver:   "oracle",
			User:     "admin",
			Password: dbPass,
			DSN:      "admin:" + dsnPass + "@tcp(adb.oraclecloud.com:1522)/tp",
		},
		Server: sampleServerConfig{
			Listen: ":443",
		},
	}

	// 1. 通过 RedactConfig 打印完整配置
	logger.Info("starting service", "config", RedactConfig(cfg))

	// 2. 直接作为属性打印敏感项（测试自动防护）
	logger.Info("db connection info", "password", dbPass, "dsn", "root:"+dsnPass+"@tcp(localhost:3306)/dash")

	// 3. 在日志消息本身内嵌入密码
	logger.Warn("failed to connect with password=" + dbPass)

	logOutput := buf.String()

	// 验证实际输出中翻不到任何明文密码
	if strings.Contains(logOutput, dbPass) {
		t.Errorf("log output contains plaintext dbPass %q\nFull logs:\n%s", dbPass, logOutput)
	}
	if strings.Contains(logOutput, dsnPass) {
		t.Errorf("log output contains plaintext dsnPass %q\nFull logs:\n%s", dsnPass, logOutput)
	}

	// 验证日志每一行都是合法 JSON
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 log lines, got %d", len(lines))
	}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("line %d is not valid JSON: %v, line: %s", i, err, line)
		}
	}
}

func TestInit_TextAndJson(t *testing.T) {
	// 测试 Init 函数调用无 panic
	Init("debug", "json")
	Init("warn", "text")
	Init("unknown", "invalid") // 回退为 info + json
}
