package linux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dash/agent/collect"
)

func init() {
	collect.Register(NewGiascanCollector())
}

const (
	defaultGiascanMaxBytes = 131072 // 128 KB
)

// GiascanCollector 采集 giascan2-router 可用节点清单 (data/available.tsv)。
// 受 root 注册与路径防逃逸限制约束，严禁外部注入任意路径。
type GiascanCollector struct {
	toolRoot string
	confPath string
	maxBytes int64
	Enabled  bool
}

// NewGiascanCollector 创建默认 giascan 采集器。
func NewGiascanCollector() *GiascanCollector {
	return NewGiascanCollectorWithRoot("/opt/tool")
}

// NewGiascanCollectorWithRoot 创建指定 toolRoot 的 giascan 采集器。
func NewGiascanCollectorWithRoot(toolRoot string) *GiascanCollector {
	return &GiascanCollector{
		toolRoot: toolRoot,
		maxBytes: defaultGiascanMaxBytes,
		Enabled:  true,
	}
}

func (c *GiascanCollector) Code() string {
	return "giascan"
}

func (c *GiascanCollector) Tier() collect.Tier {
	return collect.TierSlow
}

// CollectPayload 执行 giascan available.tsv 采集。
func (c *GiascanCollector) CollectPayload() (*collect.GiascanPayload, error) {
	if !c.Enabled {
		return nil, nil
	}

	rootPath, err := c.resolveRoot()
	if err != nil {
		errStr := err.Error()
		return &collect.GiascanPayload{
			Exists: false,
			Error:  &errStr,
		}, nil
	}

	rel := filepath.Clean("data/available.tsv")
	fullPath := filepath.Join(rootPath, rel)

	// 路径逃逸防护：解析后必须仍在 rootPath 目录内
	relCheck, err := filepath.Rel(rootPath, fullPath)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		errStr := fmt.Sprintf("路径逃逸: %s 解析后不在 %s 内", rel, rootPath)
		return &collect.GiascanPayload{
			Exists: false,
			Error:  &errStr,
		}, nil
	}

	st, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &collect.GiascanPayload{Exists: false}, nil
		}
		errStr := err.Error()
		return &collect.GiascanPayload{
			Exists: false,
			Error:  &errStr,
		}, nil
	}

	if st.IsDir() {
		return &collect.GiascanPayload{Exists: false}, nil
	}

	f, err := os.Open(fullPath)
	if err != nil {
		errStr := err.Error()
		return &collect.GiascanPayload{
			Exists: false,
			Error:  &errStr,
		}, nil
	}
	defer f.Close()

	limitReader := io.LimitReader(f, c.maxBytes)
	data, err := io.ReadAll(limitReader)
	if err != nil {
		errStr := err.Error()
		return &collect.GiascanPayload{
			Exists: false,
			Error:  &errStr,
		}, nil
	}

	size := st.Size()
	mtime := st.ModTime().Unix()
	text := string(data)

	return &collect.GiascanPayload{
		Exists: true,
		Size:   &size,
		Text:   &text,
		Mtime:  &mtime,
	}, nil
}

// Collect 执行采集并将结果写入 sample。
func (c *GiascanCollector) Collect(sample *collect.Sample) error {
	payload, err := c.CollectPayload()
	if err != nil {
		return err
	}
	if payload != nil {
		sample.SetGiascan(payload)
	}
	return nil
}

func (c *GiascanCollector) resolveRoot() (string, error) {
	if env := os.Getenv("ROOT_GIASCAN2_ROUTER"); env != "" {
		st, err := os.Stat(env)
		if err == nil && st.IsDir() {
			return filepath.Clean(env), nil
		}
	}

	dir := filepath.Join(c.toolRoot, "giascan2-router")
	st, err := os.Stat(dir)
	if err == nil && st.IsDir() {
		return filepath.Clean(dir), nil
	}

	return "", errors.New("root giascan2-router 未注册")
}
