package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestFormatFunctions(t *testing.T) {
	// FmtTime
	if got := FmtTime(int64(0)); got != "--" {
		t.Errorf("expected '--', got %s", got)
	}
	if got := FmtTime(int64(1757222400000)); !strings.Contains(got, "UTC") {
		t.Errorf("expected UTC timestamp, got %s", got)
	}

	// FmtDur
	if got := FmtDur(int64(0)); got != "0秒" {
		t.Errorf("expected '0秒', got %s", got)
	}
	if got := FmtDur(int64(-10)); got != "0秒" {
		t.Errorf("expected '0秒', got %s", got)
	}
	if got := FmtDur(int64(45)); got != "45秒" {
		t.Errorf("expected '45秒', got %s", got)
	}
	if got := FmtDur(int64(133)); got != "2分13秒" {
		t.Errorf("expected '2分13秒', got %s", got)
	}
	if got := FmtDur(int64(7980)); got != "2小时13分" {
		t.Errorf("expected '2小时13分', got %s", got)
	}
	if got := FmtDur(int64(90000)); got != "1天1小时" {
		t.Errorf("expected '1天1小时', got %s", got)
	}

	// FmtBytes
	if got := FmtBytes(int64(0)); got != "0 B" {
		t.Errorf("expected '0 B', got %s", got)
	}
	if got := FmtBytes(int64(1024)); got != "1.00 KB" {
		t.Errorf("expected '1.00 KB', got %s", got)
	}
	if got := FmtBytes(int64(1048576 * 500)); got != "500.00 MB" {
		t.Errorf("expected '500.00 MB', got %s", got)
	}
	if got := FmtBytes(int64(1073741824 * 2)); got != "2.00 GB" {
		t.Errorf("expected '2.00 GB', got %s", got)
	}

	// FmtPct
	if got := FmtPct(0.853); got != "85.3%" {
		t.Errorf("expected '85.3%%', got %s", got)
	}
	if got := FmtPct(85.3); got != "85.3%" {
		t.Errorf("expected '85.3%%', got %s", got)
	}

	// DefaultValue
	if got := DefaultValue("default", ""); got != "default" {
		t.Errorf("expected 'default', got %v", got)
	}
	if got := DefaultValue("default", "val"); got != "val" {
		t.Errorf("expected 'val', got %v", got)
	}
}

func TestTemplateEngineRender(t *testing.T) {
	engine := NewTemplateEngine("")

	data := map[string]any{
		"EventType":      "node.offline",
		"Severity":       "warning",
		"Title":          "hk-01 节点失联",
		"NodeName":       "hk-01",
		"GroupName":      "香港",
		"PublicIP":       "1.2.3.4",
		"LastSeenAtMs":   int64(1757222400000),
		"OfflineSeconds": int64(133),
	}

	rendered, err := engine.Render("node.offline", "", data)
	if err != nil {
		t.Fatalf("render node.offline failed: %v", err)
	}
	if !strings.Contains(rendered, "🔴 节点离线: hk-01") {
		t.Fatalf("unexpected render output: %s", rendered)
	}
	if !strings.Contains(rendered, "IP: 1.2.3.4") {
		t.Fatalf("missing IP in rendered output: %s", rendered)
	}
	if !strings.Contains(rendered, "已离线: 2分13秒") {
		t.Fatalf("missing duration in rendered output: %s", rendered)
	}
}

func TestTemplateEngineDegradedFallbackOnMissingVar(t *testing.T) {
	engine := NewTemplateEngine("")

	// Node offline template requires fields that are missing if we call with empty payload
	data := map[string]any{
		"EventType": "node.offline",
		"Severity":  "warning",
		"Title":     "hk-01 节点失联",
		// Missing NodeName, GroupName, etc.
	}

	rendered, err := engine.Render("node.offline", "", data)
	// Even though it failed due to missingkey=error, it must return a degraded fallback!
	if err == nil {
		t.Fatal("expected error on missing variable")
	}
	if !strings.Contains(rendered, "[WARNING] node.offline") {
		t.Fatalf("expected degraded fallback format, got: %s", rendered)
	}
	if !strings.Contains(rendered, "hk-01 节点失联") {
		t.Fatalf("expected degraded fallback title, got: %s", rendered)
	}
}

func TestTemplateEngineWhitelistFunctions(t *testing.T) {
	// Attempting to call non-whitelisted function in template must fail
	badTemplate := `{{.Title}} {{nonWhitelistedFunc "hello"}}`
	_, err := template.New("test").Funcs(FuncMap()).Parse(badTemplate)
	if err == nil {
		t.Fatal("expected error for non-whitelisted function, got nil")
	}
}

func TestTemplateEngineCustomDirOverride(t *testing.T) {
	tmpDir := t.TempDir()
	customTmpl := `CUSTOM TEMPLATE: {{.Title}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "node.offline.tmpl"), []byte(customTmpl), 0644); err != nil {
		t.Fatal(err)
	}

	engine := NewTemplateEngine(tmpDir)
	data := map[string]any{
		"EventType": "node.offline",
		"Title":     "my-title",
	}

	rendered, err := engine.Render("node.offline", "", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if rendered != "CUSTOM TEMPLATE: my-title" {
		t.Fatalf("expected custom template render, got: %s", rendered)
	}
}
