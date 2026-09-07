package runtime

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NetBaseline 记录单网卡的流量基线。
type NetBaseline struct {
	Rx   uint64 `json:"rx"`
	Tx   uint64 `json:"tx"`
	AtMs int64  `json:"at_ms"`
}

// StateData 对应 /var/lib/dash-agent/state.json 的持久化结构。
// 见 docs/03-agent.md §4。
type StateData struct {
	FactsHash     string                 `json:"facts_hash"`
	NetBaseline   map[string]NetBaseline `json:"net_baseline,omitempty"`
	MonthAnchorMs int64                  `json:"month_anchor_ms,omitempty"`
}

// StateStore 负责状态文件的原子写入与限频持久化。
type StateStore struct {
	mu            sync.Mutex
	path          string
	data          StateData
	dirty         bool
	lastWrite     time.Time
	writeInterval time.Duration
	warnedOnce    bool
}

// NewStateStore 创建状态文件管理器。
func NewStateStore(path string) *StateStore {
	return &StateStore{
		path:          path,
		writeInterval: time.Minute, // 最多每分钟写一次
	}
}

// SetWriteInterval 设置写入限频周期（仅供单元测试加速）。
func (s *StateStore) SetWriteInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeInterval = d
}

// Load 从磁盘读取状态文件。如果文件不存在或损坏，初始化干净状态。
func (s *StateStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = StateData{
		NetBaseline: make(map[string]NetBaseline),
	}
	s.dirty = false

	if s.path == "" {
		return nil
	}

	content, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read state file %s: %w", s.path, err)
	}

	var data StateData
	if err := json.Unmarshal(content, &data); err != nil {
		log.Printf("dash-agent: corrupted state file %s, resetting: %v", s.path, err)
		return nil
	}

	if data.NetBaseline == nil {
		data.NetBaseline = make(map[string]NetBaseline)
	}
	s.data = data
	return nil
}

// FactsHash 获取当前保存的 facts_hash。
func (s *StateStore) FactsHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.FactsHash
}

// UpdateFactsHash 更新 facts_hash。若发生变化，标记为 dirty 并返回 true。
func (s *StateStore) UpdateFactsHash(h string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.FactsHash == h {
		return false
	}
	s.data.FactsHash = h
	s.dirty = true
	return true
}

// UpdateNetBaseline 更新指定网卡的流量基线。
func (s *StateStore) UpdateNetBaseline(nic string, rx, tx uint64, atMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.NetBaseline == nil {
		s.data.NetBaseline = make(map[string]NetBaseline)
	}
	curr, exists := s.data.NetBaseline[nic]
	if exists && curr.Rx == rx && curr.Tx == tx && curr.AtMs == atMs {
		return
	}

	s.data.NetBaseline[nic] = NetBaseline{
		Rx:   rx,
		Tx:   tx,
		AtMs: atMs,
	}
	s.dirty = true
}

// FlushIfDue 检查是否已达到限频周期（默认 ≥ 1 分钟），若已到期且 dirty 则执行原子写入。
func (s *StateStore) FlushIfDue() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}
	if time.Since(s.lastWrite) < s.writeInterval {
		return nil
	}
	return s.atomicWriteLocked()
}

// Flush 立即将脏数据写入磁盘（忽略时间间隔限制，通常在优雅关机时调用）。
func (s *StateStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}
	return s.atomicWriteLocked()
}

// atomicWriteLocked 执行写临时文件 → fsync → rename 原子替换。
// 必须持有 s.mu。
func (s *StateStore) atomicWriteLocked() error {
	if s.path == "" {
		s.dirty = false
		return nil
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if !s.warnedOnce {
			log.Printf("dash-agent: cannot create state directory %s: %v", dir, err)
			s.warnedOnce = true
		}
		return err
	}

	dataBytes, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		if !s.warnedOnce {
			log.Printf("dash-agent: cannot create temp state file in %s: %v", dir, err)
			s.warnedOnce = true
		}
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(dataBytes); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("atomic rename state file: %w", err)
	}

	s.dirty = false
	s.lastWrite = time.Now()
	return nil
}
