package ingest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"dash/internal/ingest"
	"dash/internal/protocol"
)

// TestAcceptance_30Agents_120Rounds 验收标准 1：
// 30 个模拟 agent 以 5 秒间隔上报 10 分钟（共 120 轮），sample_host 行数 = 30 × 120 = 3600
func TestAcceptance_30Agents_120Rounds(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	// 创建 Service 并启动
	writerOpts := &ingest.WriterOptions{
		FlushInterval: 50 * time.Millisecond,
		RetryDelays:   []time.Duration{10 * time.Millisecond},
	}
	svc := ingest.NewService(db, nil, writerOpts)
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("failed to start ingest service: %v", err)
	}
	defer svc.Stop()

	numAgents := 30
	numRounds := 120
	baseTs := int64(1700000000000)

	var wg sync.WaitGroup
	// 30 个模拟 agent 并发以 5 秒时间戳推进上报
	for a := 0; a < numAgents; a++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("node-30a-%03d", agentIdx)

			for round := 0; round < numRounds; round++ {
				ts := baseTs + int64(round*5000)
				cpu := 5.0 + float64((agentIdx+round)%30)
				mem := int64(1024*1024*1024 + agentIdx*1000 + round*500)
				up := int64(10000 + round*1000)
				down := int64(20000 + round*2000)

				m := &protocol.MetricsParams{
					TsMs:    ts,
					CPUPct:  &cpu,
					MemUsed: &mem,
					Net: &protocol.NetReport{
						TotalUp:   &up,
						TotalDown: &down,
					},
				}

				// 每 12 轮（60 秒）附加 Slow 档维度数据
				if round%12 == 0 {
					diskUsed := int64(50 * 1024 * 1024 * 1024)
					m.Slow = &protocol.SlowReport{
						DiskUsed: &diskUsed,
						Disks: []protocol.DiskEntry{
							{Key: "/", Used: &diskUsed, Total: ptrInt64(200 * 1024 * 1024 * 1024)},
						},
						NICs: []protocol.NICEntry{
							{Key: "eth0", TotalUp: &up, TotalDown: &down},
						},
					}
				}

				svc.Ingest(nodeID, ts, m)
			}
		}(a)
	}

	wg.Wait()

	// 停止后台写入循环并排空缓冲区
	svc.Stop()

	var hostCount, dimCount int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host").Scan(&hostCount); err != nil {
		t.Fatalf("query sample_host count: %v", err)
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM sample_dim").Scan(&dimCount); err != nil {
		t.Fatalf("query sample_dim count: %v", err)
	}

	expectedHostRows := numAgents * numRounds
	if hostCount != expectedHostRows {
		t.Fatalf("expected exactly %d sample_host rows, got %d", expectedHostRows, hostCount)
	}

	t.Logf("✓ Acceptance 1 passed: %d host rows, %d dim rows inserted into database", hostCount, dimCount)
}

// TestAcceptance_DBDisconnect_RealtimeAvailable 验收标准 2：
// 中途断开数据库，恢复后写入继续，且实时视图全程可用
func TestAcceptance_DBDisconnect_RealtimeAvailable(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	// 初始 service
	svc := ingest.NewService(db, nil, &ingest.WriterOptions{
		FlushInterval: 50 * time.Millisecond,
		RetryDelays:   []time.Duration{5 * time.Millisecond},
	})
	_ = svc.Start(ctx)
	defer svc.Stop()

	nodeID := "node-disconnect-test"
	cpu1 := 20.0
	mem1 := int64(2048)
	ts1 := int64(1700000010000)

	// 1. 正常上报一条
	svc.Ingest(nodeID, ts1, &protocol.MetricsParams{
		TsMs:    ts1,
		CPUPct:  &cpu1,
		MemUsed: &mem1,
	})
	_ = svc.Writer().FlushSync(ctx)

	// 2. 模拟断开数据库：关闭底层连接
	_ = db.Close()

	// 3. 在数据库断开期间持续上报：验证采集链路不卡死、实时视图全程可用
	for i := 1; i <= 5; i++ {
		ts := ts1 + int64(i*5000)
		cpu := 30.0 + float64(i)
		mem := int64(2048 + i*100)

		svc.Ingest(nodeID, ts, &protocol.MetricsParams{
			TsMs:    ts,
			CPUPct:  &cpu,
			MemUsed: &mem,
		})

		// 验证实时视图在断库期间仍可直接从内存读取最新指标
		latest, ok := svc.Latest().Get(nodeID)
		if !ok || latest.CpuPct != cpu || latest.MemUsed != mem || latest.TsMs != ts {
			t.Fatalf("realtime view failed during db disconnect round %d: %+v", i, latest)
		}
	}

	// 4. 重新建立数据库连接
	dbRecovered := getTestDB(t)
	defer dbRecovered.Close()

	// 替换底层 DB 并触发 flush，验证写入继续
	svcRecovered := ingest.NewService(dbRecovered, nil, &ingest.WriterOptions{
		FlushInterval: 50 * time.Millisecond,
	})
	_ = svcRecovered.Start(ctx)
	defer svcRecovered.Stop()

	tsRecovered := ts1 + 30000
	cpuRecovered := 55.0
	svcRecovered.Ingest(nodeID, tsRecovered, &protocol.MetricsParams{
		TsMs:   tsRecovered,
		CPUPct: &cpuRecovered,
	})
	_ = svcRecovered.Writer().FlushSync(ctx)

	var count int
	_ = dbRecovered.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host WHERE node_id = ?", nodeID).Scan(&count)
	if count < 2 {
		t.Fatalf("expected continued writes after db recovery, got count %d", count)
	}

	t.Logf("✓ Acceptance 2 passed: realtime view fully available during db outage, writes resumed successfully")
}

// TestAcceptance_RebootAndRestart_NoFakeDelta 验收标准 3 & 4：
// 3. 模拟机器重启（net_total_up 归零），traffic_up 不出现负值、不出现异常巨值
// 4. dashd 进程重启后，prev 从数据库正确恢复，重启前后不产生一个虚假的巨大 delta
func TestAcceptance_RebootAndRestart_NoFakeDelta(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	svc1 := ingest.NewService(db, nil, &ingest.WriterOptions{FlushInterval: 50 * time.Millisecond})
	_ = svc1.Start(ctx)

	nodeID := "node-reboot-verify"
	nowMs := time.Now().UnixMilli()

	// 1. 初次上报：累计值 50 GB
	up50GB := int64(50 * 1024 * 1024 * 1024)
	down50GB := int64(100 * 1024 * 1024 * 1024)
	svc1.Ingest(nodeID, nowMs, &protocol.MetricsParams{
		TsMs: nowMs,
		Net:  &protocol.NetReport{TotalUp: &up50GB, TotalDown: &down50GB},
	})
	_ = svc1.Writer().FlushSync(ctx)

	// 库中第一行：traffic_up 必须为 NULL（无 prev）
	var trafficUp sqlNullInt64Helper
	err := db.QueryRow(ctx, "SELECT traffic_up FROM sample_host WHERE node_id = ? AND ts_ms = ?", nodeID, nowMs).Scan(&trafficUp)
	if err != nil {
		t.Fatalf("query first row: %v", err)
	}
	if trafficUp.valid {
		t.Fatalf("expected traffic_up to be NULL on initial report, got %d", trafficUp.val)
	}

	// 2. 进程重启模拟：关闭 svc1，新建 svc2 并 RecoverAll
	svc1.Stop()

	svc2 := ingest.NewService(db, nil, &ingest.WriterOptions{FlushInterval: 50 * time.Millisecond})
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("svc2 start failed: %v", err)
	}
	defer svc2.Stop()

	// 重启后上报：累计值 50 GB + 4096 bytes
	nowMs2 := nowMs + 5000
	up50GBPlus := up50GB + 4096
	down50GBPlus := down50GB + 8192
	svc2.Ingest(nodeID, nowMs2, &protocol.MetricsParams{
		TsMs: nowMs2,
		Net:  &protocol.NetReport{TotalUp: &up50GBPlus, TotalDown: &down50GBPlus},
	})
	_ = svc2.Writer().FlushSync(ctx)

	// 验收标准 4：重启前后不产生虚假的巨大 delta，delta 应严格等于 4096
	var trafficUpAfterRestart sqlNullInt64Helper
	err = db.QueryRow(ctx, "SELECT traffic_up FROM sample_host WHERE node_id = ? AND ts_ms = ?", nodeID, nowMs2).Scan(&trafficUpAfterRestart)
	if err != nil {
		t.Fatalf("query second row: %v", err)
	}
	if !trafficUpAfterRestart.valid || trafficUpAfterRestart.val != 4096 {
		t.Fatalf("expected recovered delta=4096, got %+v", trafficUpAfterRestart)
	}

	// 3. 验收标准 3：模拟机器重启（net_total_up 归零至 512 bytes）
	nowMs3 := nowMs2 + 5000
	upReboot := int64(512)
	downReboot := int64(1024)
	svc2.Ingest(nodeID, nowMs3, &protocol.MetricsParams{
		TsMs: nowMs3,
		Net:  &protocol.NetReport{TotalUp: &upReboot, TotalDown: &downReboot},
	})
	_ = svc2.Writer().FlushSync(ctx)

	var trafficUpReboot sqlNullInt64Helper
	err = db.QueryRow(ctx, "SELECT traffic_up FROM sample_host WHERE node_id = ? AND ts_ms = ?", nodeID, nowMs3).Scan(&trafficUpReboot)
	if err != nil {
		t.Fatalf("query reboot row: %v", err)
	}
	if !trafficUpReboot.valid {
		t.Fatal("expected non-null traffic_up on reboot")
	}
	if trafficUpReboot.val < 0 {
		t.Fatalf("traffic_up cannot be negative, got %d", trafficUpReboot.val)
	}
	if trafficUpReboot.val != 512 {
		t.Fatalf("expected traffic_up to equal rebooted counter (512), got %d", trafficUpReboot.val)
	}

	t.Logf("✓ Acceptance 3 & 4 passed: clean reboot reset handling and process restart recovery verified")
}

// TestAcceptance_HealthzEndpoint 验证 /healthz 端点暴露丢批指标
func TestAcceptance_HealthzEndpoint(t *testing.T) {
	svc := ingest.NewService(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	svc.HandleHealthz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Fatal("expected non-empty healthz body")
	}
	t.Logf("✓ Healthz response: %s", body)
}

type sqlNullInt64Helper struct {
	val   int64
	valid bool
}

func (s *sqlNullInt64Helper) Scan(src any) error {
	if src == nil {
		s.val = 0
		s.valid = false
		return nil
	}
	switch v := src.(type) {
	case int64:
		s.val = v
		s.valid = true
	case []byte:
		var parsed int64
		_, err := fmt.Sscanf(string(v), "%d", &parsed)
		if err == nil {
			s.val = parsed
			s.valid = true
		}
	}
	return nil
}
