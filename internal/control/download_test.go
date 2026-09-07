package control_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dash/internal/config"
	"dash/internal/control"
)

func TestInstallScriptHandler(t *testing.T) {
	cfg := config.DefaultConfig()
	handler := control.NewInstallScriptHandler(cfg, nil)

	// 1. 普通 GET /install.sh
	req := httptest.NewRequest("GET", "http://dash.example.com/install.sh", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "#!/bin/sh") {
		t.Errorf("expected #!/bin/sh in response, got: %s", body[:min(len(body), 100)])
	}
	if !strings.Contains(body, `DEFAULT_ENDPOINT="https://dash.example.com"`) {
		t.Errorf("expected default endpoint https://dash.example.com in response, got: %s", body)
	}

	// 2. 带 ?token=my-token 参数
	reqWithToken := httptest.NewRequest("GET", "http://dash.example.com/install.sh?token=test-enroll-token-123", nil)
	recWithToken := httptest.NewRecorder()
	handler.ServeHTTP(recWithToken, reqWithToken)

	if recWithToken.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recWithToken.Code)
	}
	bodyWithToken := recWithToken.Body.String()
	if !strings.Contains(bodyWithToken, `DEFAULT_TOKEN="test-enroll-token-123"`) {
		t.Errorf("expected default token test-enroll-token-123 in response, got: %s", bodyWithToken)
	}

	// 3. POST 请求返回 405 Method Not Allowed
	postReq := httptest.NewRequest("POST", "http://dash.example.com/install.sh", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", postRec.Code)
	}
}

func TestInstallScriptWithDB(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UnixMilli()
	upsertSQL := database.Dialect().UpsertSQL("settings", []string{"setting_key"}, []string{"setting_val", "updated_at_ms"})
	_, err := database.Exec(ctx, upsertSQL, "site.domain", "custom.domain.org", now)
	if err != nil {
		t.Fatalf("insert setting failed: %v", err)
	}

	cfg := config.DefaultConfig()
	handler := control.NewInstallScriptHandler(cfg, database)

	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/install.sh", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `DEFAULT_ENDPOINT="https://custom.domain.org"`) {
		t.Errorf("expected configured domain https://custom.domain.org, got: %s", body)
	}
}

func TestDownloadHandler(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DASH_DL_DIR", tempDir)

	// 创建模拟二进制产物
	amd64Content := []byte("binary-dash-agent-amd64-payload")
	amd64Path := filepath.Join(tempDir, "dash-agent-linux-amd64")
	if err := os.WriteFile(amd64Path, amd64Content, 0755); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	h := sha256.Sum256(amd64Content)
	expectedSHA := hex.EncodeToString(h[:])

	dlHandler := control.NewDownloadHandler(config.DefaultConfig())

	// 1. 下载二进制
	req := httptest.NewRequest("GET", "http://dash.example.com/dl/dash-agent-linux-amd64", nil)
	rec := httptest.NewRecorder()
	dlHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(amd64Content) {
		t.Errorf("expected %s, got %s", string(amd64Content), rec.Body.String())
	}

	// 2. 动态计算 sha256sums.txt
	reqSha := httptest.NewRequest("GET", "http://dash.example.com/dl/sha256sums.txt", nil)
	recSha := httptest.NewRecorder()
	dlHandler.ServeHTTP(recSha, reqSha)

	if recSha.Code != http.StatusOK {
		t.Fatalf("expected 200 for sha256sums.txt, got %d", recSha.Code)
	}
	shaContent := recSha.Body.String()
	if !strings.Contains(shaContent, expectedSHA) || !strings.Contains(shaContent, "dash-agent-linux-amd64") {
		t.Errorf("sha256sums.txt missing expected sha: %s", shaContent)
	}

	// 3. 动态计算单个 .sha256 文件
	reqSingleSha := httptest.NewRequest("GET", "http://dash.example.com/dl/dash-agent-linux-amd64.sha256", nil)
	recSingleSha := httptest.NewRecorder()
	dlHandler.ServeHTTP(recSingleSha, reqSingleSha)

	if recSingleSha.Code != http.StatusOK {
		t.Fatalf("expected 200 for .sha256, got %d", recSingleSha.Code)
	}
	if !strings.Contains(recSingleSha.Body.String(), expectedSHA) {
		t.Errorf(".sha256 missing expected sha: %s", recSingleSha.Body.String())
	}

	// 4. 不存在的文件 404
	req404 := httptest.NewRequest("GET", "http://dash.example.com/dl/not-exist", nil)
	rec404 := httptest.NewRecorder()
	dlHandler.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec404.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
