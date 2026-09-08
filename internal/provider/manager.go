package provider

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"dash/internal/logx"
)

type ProcessInstance struct {
	providerCode string
	execPath     string
	socketPath   string
	cmd          *exec.Cmd
	client       ProviderClient
	stopCh       chan struct{}
}

// Manager supervises provider plugin processes and manages RPC clients.
type Manager struct {
	mu           sync.RWMutex
	runtimeDir   string
	searchDirs   []string
	instances    map[string]*ProcessInstance
	mockClients  map[string]ProviderClient
	closed       bool
}

// NewManager creates a provider process manager.
func NewManager(runtimeDir string, searchDirs ...string) *Manager {
	if runtimeDir == "" {
		runtimeDir = os.TempDir()
	}
	if len(searchDirs) == 0 {
		searchDirs = []string{"bin", "/usr/local/bin"}
	}
	return &Manager{
		runtimeDir:  runtimeDir,
		searchDirs:  searchDirs,
		instances:   make(map[string]*ProcessInstance),
		mockClients: make(map[string]ProviderClient),
	}
}

// RegisterMock registers a mock or custom provider client (used in tests).
func (m *Manager) RegisterMock(providerCode string, client ProviderClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockClients[providerCode] = client
}

// ResolveExecPath resolves the given execPath according to the rules:
// 1. 绝对路径直接用（若存在则直接使用；若不存在则退回可执行文件所在目录与 PATH 查找）
// 2. 相对路径先按可执行文件所在目录解析 → 再退回 PATH 查找
func ResolveExecPath(execPath string) (string, error) {
	if filepath.IsAbs(execPath) {
		if fi, err := os.Stat(execPath); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			return execPath, nil
		}
		// 若指定的绝对路径在磁盘上不存在（如在开发机运行但配置为生产绝对路径），退回本进程所在目录与 PATH 查找
	}

	baseName := filepath.Base(execPath)

	// 先按可执行文件 (dashd) 所在目录解析
	if selfExe, err := os.Executable(); err == nil {
		selfDir := filepath.Dir(selfExe)
		candidates := []string{
			filepath.Join(selfDir, execPath),
			filepath.Join(selfDir, baseName),
			filepath.Join(filepath.Dir(selfDir), execPath),
			filepath.Join(filepath.Dir(selfDir), "bin", baseName),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
				return c, nil
			}
		}
	}

	// 检查当前工作目录 (CWD)
	if fi, err := os.Stat(execPath); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
		return execPath, nil
	}

	// 再退回 PATH 查找
	if p, err := exec.LookPath(execPath); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath(baseName); err == nil {
		return p, nil
	}

	if filepath.IsAbs(execPath) {
		return execPath, nil
	}

	return "", fmt.Errorf("provider: executable %q not found", execPath)
}

// FindExecutable searches for the provider executable in search directories.
func (m *Manager) FindExecutable(providerCode string) (string, error) {
	binName := fmt.Sprintf("dash-provider-%s", providerCode)

	for _, dir := range m.searchDirs {
		p := filepath.Join(dir, binName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			return p, nil
		}
	}

	return ResolveExecPath(binName)
}

// GetClient returns a connected ProviderClient, launching and supervising the process if needed.
// An optional explicit execPath can be passed; if omitted, FindExecutable(providerCode) is used.
func (m *Manager) GetClient(ctx context.Context, providerCode string, optExecPath ...string) (ProviderClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, fmt.Errorf("provider manager is closed")
	}

	if mock, ok := m.mockClients[providerCode]; ok {
		return mock, nil
	}

	if inst, ok := m.instances[providerCode]; ok && inst.client != nil {
		return inst.client, nil
	}

	var execPath string
	if len(optExecPath) > 0 && optExecPath[0] != "" {
		execPath = optExecPath[0]
	} else {
		var err error
		execPath, err = m.FindExecutable(providerCode)
		if err != nil {
			return nil, err
		}
	}

	socketPath := filepath.Join(m.runtimeDir, fmt.Sprintf("dash-provider-%s-%d.sock", providerCode, os.Getpid()))
	_ = os.Remove(socketPath)

	inst, err := m.startProcess(ctx, providerCode, execPath, socketPath)
	if err != nil {
		return nil, err
	}

	m.instances[providerCode] = inst
	return inst.client, nil
}

func (m *Manager) startProcess(ctx context.Context, providerCode, execPath, socketPath string) (*ProcessInstance, error) {
	resolvedPath, err := ResolveExecPath(execPath)
	if err != nil {
		return nil, fmt.Errorf("provider: resolve %s failed: %w", execPath, err)
	}

	cmd := exec.Command(resolvedPath, "-socket", socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("provider: start %s failed: %w", resolvedPath, err)
	}

	// Wait for socket to become active (up to 5 seconds)
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !ready {
		_ = cmd.Process.Kill()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("provider: process %s started but socket %s was not ready in 5s", execPath, socketPath)
	}

	client := NewRPCClient(socketPath)
	stopCh := make(chan struct{})

	inst := &ProcessInstance{
		providerCode: providerCode,
		execPath:     execPath,
		socketPath:   socketPath,
		cmd:          cmd,
		client:       client,
		stopCh:       stopCh,
	}

	// Supervise
	go func() {
		err := cmd.Wait()
		select {
		case <-stopCh:
			return
		default:
			logx.Warn("provider process exited unexpectedly", "provider", providerCode, "err", err)
			m.mu.Lock()
			delete(m.instances, providerCode)
			_ = os.Remove(socketPath)
			m.mu.Unlock()
		}
	}()

	return inst, nil
}

// Close stops all provider processes.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	for code, inst := range m.instances {
		close(inst.stopCh)
		if inst.client != nil {
			_ = inst.client.Close()
		}
		if inst.cmd != nil && inst.cmd.Process != nil {
			_ = inst.cmd.Process.Kill()
		}
		_ = os.Remove(inst.socketPath)
		delete(m.instances, code)
	}
	return nil
}
