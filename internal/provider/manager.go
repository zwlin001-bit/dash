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

// FindExecutable searches for the provider executable in search directories.
func (m *Manager) FindExecutable(providerCode string) (string, error) {
	binName := fmt.Sprintf("dash-provider-%s", providerCode)

	for _, dir := range m.searchDirs {
		p := filepath.Join(dir, binName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			return p, nil
		}
	}

	// Try PATH
	if p, err := exec.LookPath(binName); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("provider: binary %q not found in %v or PATH", binName, m.searchDirs)
}

// GetClient returns a connected ProviderClient, launching and supervising the process if needed.
func (m *Manager) GetClient(ctx context.Context, providerCode string) (ProviderClient, error) {
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

	execPath, err := m.FindExecutable(providerCode)
	if err != nil {
		return nil, err
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
	cmd := exec.Command(execPath, "-socket", socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("provider: start %s failed: %w", execPath, err)
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
