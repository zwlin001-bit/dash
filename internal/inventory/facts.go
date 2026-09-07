package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"dash/internal/audit"
	"dash/internal/db"
)

// NodeFacts 对应 node_facts 表，记录节点的静态规格与操作系统信息。
type NodeFacts struct {
	NodeID      string  `json:"node_id"`
	Arch        *string `json:"arch,omitempty"`
	OSName      *string `json:"os_name,omitempty"`
	OSVersion   *string `json:"os_version,omitempty"`
	Kernel      *string `json:"kernel,omitempty"`
	Virt        *string `json:"virt,omitempty"`
	CPUModel    *string `json:"cpu_model,omitempty"`
	CPUCores    *int    `json:"cpu_cores,omitempty"`
	CPUThreads  *int    `json:"cpu_threads,omitempty"`
	MemTotal    *int64  `json:"mem_total,omitempty"`
	SwapTotal   *int64  `json:"swap_total,omitempty"`
	DiskTotal   *int64  `json:"disk_total,omitempty"`
	IPv4        *string `json:"ipv4,omitempty"`
	IPv6        *string `json:"ipv6,omitempty"`
	BootAtMs    *int64  `json:"boot_at_ms,omitempty"`
	FactsHash   *string `json:"facts_hash,omitempty"`
	UpdatedAtMs int64   `json:"updated_at_ms"`
}

// FactsService 提供节点规格信息的查询与写入接口。
type FactsService struct {
	db *db.DB
}

func NewFactsService(database *db.DB) *FactsService {
	return &FactsService{db: database}
}

// GetFacts 获取指定节点的静态事实信息。
func (s *FactsService) GetFacts(ctx context.Context, nodeID string) (*NodeFacts, error) {
	q := `SELECT node_id, arch, os_name, os_version, kernel, virt, cpu_model, cpu_cores, cpu_threads,
mem_total, swap_total, disk_total, ipv4, ipv6, boot_at_ms, facts_hash, updated_at_ms
FROM node_facts WHERE node_id = ?`

	var f NodeFacts
	var arch, osName, osVer, kernel, virt, cpuModel, ipv4, ipv6, factsHash sql.NullString
	var cpuCores, cpuThreads sql.NullInt32
	var memTotal, swapTotal, diskTotal, bootAt sql.NullInt64

	row := s.db.QueryRow(ctx, q, nodeID)
	err := row.Scan(
		&f.NodeID, &arch, &osName, &osVer, &kernel, &virt, &cpuModel, &cpuCores, &cpuThreads,
		&memTotal, &swapTotal, &diskTotal, &ipv4, &ipv6, &bootAt, &factsHash, &f.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}

	if arch.Valid {
		f.Arch = &arch.String
	}
	if osName.Valid {
		f.OSName = &osName.String
	}
	if osVer.Valid {
		f.OSVersion = &osVer.String
	}
	if kernel.Valid {
		f.Kernel = &kernel.String
	}
	if virt.Valid {
		f.Virt = &virt.String
	}
	if cpuModel.Valid {
		f.CPUModel = &cpuModel.String
	}
	if cpuCores.Valid {
		v := int(cpuCores.Int32)
		f.CPUCores = &v
	}
	if cpuThreads.Valid {
		v := int(cpuThreads.Int32)
		f.CPUThreads = &v
	}
	if memTotal.Valid {
		f.MemTotal = &memTotal.Int64
	}
	if swapTotal.Valid {
		f.SwapTotal = &swapTotal.Int64
	}
	if diskTotal.Valid {
		f.DiskTotal = &diskTotal.Int64
	}
	if ipv4.Valid {
		f.IPv4 = &ipv4.String
	}
	if ipv6.Valid {
		f.IPv6 = &ipv6.String
	}
	if bootAt.Valid {
		f.BootAtMs = &bootAt.Int64
	}
	if factsHash.Valid {
		f.FactsHash = &factsHash.String
	}

	return &f, nil
}

// SaveFacts 写入或更新节点事实规格（可移植 SQL）。
func (s *FactsService) SaveFacts(ctx context.Context, f *NodeFacts, actorKind, actorID, ip string) error {
	if f == nil || f.NodeID == "" {
		return errors.New("missing node_id")
	}

	now := time.Now().UnixMilli()
	f.UpdatedAtMs = now

	var exists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_facts WHERE node_id = ?`, f.NodeID).Scan(&exists)

	var err error
	if exists > 0 {
		q := `UPDATE node_facts SET
arch = ?, os_name = ?, os_version = ?, kernel = ?, virt = ?, cpu_model = ?,
cpu_cores = ?, cpu_threads = ?, mem_total = ?, swap_total = ?, disk_total = ?,
ipv4 = ?, ipv6 = ?, boot_at_ms = ?, facts_hash = ?, updated_at_ms = ?
WHERE node_id = ?`
		_, err = s.db.Exec(ctx, q,
			f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt, f.CPUModel,
			f.CPUCores, f.CPUThreads, f.MemTotal, f.SwapTotal, f.DiskTotal,
			f.IPv4, f.IPv6, f.BootAtMs, f.FactsHash, f.UpdatedAtMs,
			f.NodeID,
		)
	} else {
		q := `INSERT INTO node_facts (
node_id, arch, os_name, os_version, kernel, virt, cpu_model,
cpu_cores, cpu_threads, mem_total, swap_total, disk_total,
ipv4, ipv6, boot_at_ms, facts_hash, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err = s.db.Exec(ctx, q,
			f.NodeID, f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt, f.CPUModel,
			f.CPUCores, f.CPUThreads, f.MemTotal, f.SwapTotal, f.DiskTotal,
			f.IPv4, f.IPv6, f.BootAtMs, f.FactsHash, f.UpdatedAtMs,
		)
	}

	if err != nil {
		return fmt.Errorf("save facts: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.facts_save",
		TargetKind: "node",
		TargetID:   f.NodeID,
		Result:     "ok",
		IP:         ip,
	})

	return nil
}

func (s *FactsService) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	facts, err := s.GetFacts(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "facts_not_found", "节点事实信息尚未上报", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "获取节点事实信息失败", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, facts)
}

func (s *FactsService) HandlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	var f NodeFacts
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}
	f.NodeID = id

	actorKind, actorID, ip := ActorInfo(r)
	if err := s.SaveFacts(r.Context(), &f, actorKind, actorID, ip); err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "保存节点事实信息失败", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, f)
}
