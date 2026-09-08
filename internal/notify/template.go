package notify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"dash/internal/notify/templates"
)

// FuncMap defines the strict whitelist of template functions (09-events-notify.md §5.1).
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"fmtTime":  FmtTime,
		"fmtDur":   FmtDur,
		"fmtBytes": FmtBytes,
		"fmtPct":   FmtPct,
		"default":  DefaultValue,
	}
}

// FmtTime formats epoch milliseconds to a human-readable UTC timestamp.
func FmtTime(v any) string {
	var ms int64
	switch n := v.(type) {
	case int64:
		ms = n
	case int:
		ms = int64(n)
	case float64:
		ms = int64(n)
	default:
		return "--"
	}
	if ms <= 0 {
		return "--"
	}
	t := time.UnixMilli(ms).UTC()
	return t.Format("2006-01-02 15:04:05 UTC")
}

// FmtDur formats seconds into a human-readable duration (e.g. "2小时13分", "45秒").
func FmtDur(v any) string {
	var sec int64
	switch n := v.(type) {
	case int64:
		sec = n
	case int:
		sec = int64(n)
	case float64:
		sec = int64(n)
	default:
		return "--"
	}

	if sec <= 0 {
		return "0秒"
	}

	days := sec / 86400
	hours := (sec % 86400) / 3600
	mins := (sec % 3600) / 60
	secs := sec % 60

	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%d天%d小时", days, hours)
		}
		return fmt.Sprintf("%d天", days)
	}
	if hours > 0 {
		if mins > 0 {
			return fmt.Sprintf("%d小时%d分", hours, mins)
		}
		return fmt.Sprintf("%d小时", hours)
	}
	if mins > 0 {
		if secs > 0 {
			return fmt.Sprintf("%d分%d秒", mins, secs)
		}
		return fmt.Sprintf("%d分", mins)
	}
	return fmt.Sprintf("%d秒", secs)
}

// FmtBytes formats a byte count into a human-readable string (e.g. "1.2 GB", "500 MB").
func FmtBytes(v any) string {
	var b float64
	switch n := v.(type) {
	case int64:
		b = float64(n)
	case int:
		b = float64(n)
	case float64:
		b = n
	default:
		return "--"
	}

	if b <= 0 {
		return "0 B"
	}

	const (
		kb = 1024.0
		mb = kb * 1024.0
		gb = mb * 1024.0
		tb = gb * 1024.0
	)

	switch {
	case b >= tb:
		return fmt.Sprintf("%.2f TB", b/tb)
	case b >= gb:
		return fmt.Sprintf("%.2f GB", b/gb)
	case b >= mb:
		return fmt.Sprintf("%.2f MB", b/mb)
	case b >= kb:
		return fmt.Sprintf("%.2f KB", b/kb)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

// FmtPct formats a number or decimal into a percentage string (e.g. "85.3%").
func FmtPct(v any) string {
	var p float64
	switch n := v.(type) {
	case float64:
		p = n
	case int64:
		p = float64(n)
	case int:
		p = float64(n)
	default:
		return "--"
	}

	// If given as ratio (0 <= p <= 1), convert to percentage
	if p > 0 && p <= 1.0 {
		p *= 100.0
	}
	return fmt.Sprintf("%.1f%%", p)
}

// DefaultValue returns defVal if val is nil, empty string, or zero.
func DefaultValue(defVal any, val any) any {
	if val == nil {
		return defVal
	}
	switch v := val.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return defVal
		}
		return v
	case int:
		if v == 0 {
			return defVal
		}
		return v
	case int64:
		if v == 0 {
			return defVal
		}
		return v
	case float64:
		if v == 0 {
			return defVal
		}
		return v
	default:
		return val
	}
}

// TemplateEngine manages loading, caching, and rendering of notification templates.
type TemplateEngine struct {
	customDir string
}

// NewTemplateEngine creates an engine optionally backed by a custom disk directory.
func NewTemplateEngine(customDir string) *TemplateEngine {
	return &TemplateEngine{
		customDir: customDir,
	}
}

// loadTemplateContent finds template content by template name, falling back to embedded templates.
func (e *TemplateEngine) loadTemplateContent(name string) (string, error) {
	if !strings.HasSuffix(name, ".tmpl") {
		name = name + ".tmpl"
	}

	// 1. Try custom disk directory if specified
	if e.customDir != "" {
		diskPath := filepath.Join(e.customDir, name)
		if data, err := os.ReadFile(diskPath); err == nil {
			return string(data), nil
		}
	}

	// 2. Try embedded templates
	if data, err := templates.FS.ReadFile(name); err == nil {
		return string(data), nil
	}

	return "", fmt.Errorf("template %s not found", name)
}

// Render executes the matching template for the given event.
// Lookup precedence:
//  1. templateName (if non-empty)
//  2. eventType + ".tmpl"
//  3. "_default.tmpl"
//
// If execution fails or variable is missing, falls back to degraded title text
// per P2-01: "模板渲染失败必须降级成「事件类型 + 标题」推出去，不许因为模板错就不推".
func (e *TemplateEngine) Render(eventType, templateName string, data map[string]any) (rendered string, err error) {
	var content string
	var loadErr error

	// 1. Custom templateName
	if templateName != "" {
		content, loadErr = e.loadTemplateContent(templateName)
	}

	// 2. Event type specific template
	if content == "" && eventType != "" {
		content, loadErr = e.loadTemplateContent(eventType)
	}

	// 3. Fallback to default template
	if content == "" {
		content, loadErr = e.loadTemplateContent("_default")
	}

	if content == "" {
		return e.DegradedFallback(eventType, data), fmt.Errorf("load template failed: %w", loadErr)
	}

	tmpl, parseErr := template.New("notify").Funcs(FuncMap()).Option("missingkey=error").Parse(content)
	if parseErr != nil {
		return e.DegradedFallback(eventType, data), fmt.Errorf("parse template failed: %w", parseErr)
	}

	var buf bytes.Buffer
	if execErr := tmpl.Execute(&buf, data); execErr != nil {
		return e.DegradedFallback(eventType, data), fmt.Errorf("execute template failed: %w", execErr)
	}

	return strings.TrimSpace(buf.String()), nil
}

// DegradedFallback generates a safe fallback string when template parsing/rendering fails.
func (e *TemplateEngine) DegradedFallback(eventType string, data map[string]any) string {
	severity, _ := data["Severity"].(string)
	if severity == "" {
		severity = "info"
	}
	title, _ := data["Title"].(string)
	if title == "" {
		title = eventType
	}
	return fmt.Sprintf("[%s] %s\n%s", strings.ToUpper(severity), eventType, title)
}
