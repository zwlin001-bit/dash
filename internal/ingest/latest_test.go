package ingest_test

import (
	"testing"

	"dash/internal/ingest"
	"dash/internal/protocol"
)

func TestLatestCache_LifecycleAndPreservation(t *testing.T) {
	cache := ingest.NewLatestCache()
	nodeID := "node-latest-001"

	// 1. 初始查询应未命中
	_, ok := cache.Get(nodeID)
	if ok {
		t.Fatal("expected cache miss for uninitialized node")
	}

	// 2. 注入静态 facts (MemTotal, DiskTotal)
	cache.SetMemTotal(nodeID, 16*1024*1024*1024)
	cache.SetDiskTotal(nodeID, 500*1024*1024*1024)

	var notified ingest.NodeLatest
	notifyCalls := 0
	cache.OnUpdate(func(nid string, l ingest.NodeLatest) {
		if nid == nodeID {
			notified = l
			notifyCalls++
		}
	})

	// 3. 第一轮上报 (Fast 档 + Slow 档)
	cpu := 12.5
	memUsed := int64(4 * 1024 * 1024 * 1024)
	upBps := int64(102400)
	downBps := int64(204800)
	diskUsed := int64(150 * 1024 * 1024 * 1024)
	tcpCnt := int32(42)

	m1 := &protocol.MetricsParams{
		TsMs:    1700000000000,
		CPUPct:  &cpu,
		MemUsed: &memUsed,
		Net: &protocol.NetReport{
			UpBps:   &upBps,
			DownBps: &downBps,
		},
		Slow: &protocol.SlowReport{
			DiskUsed: &diskUsed,
			TCPCount: &tcpCnt,
			Disks: []protocol.DiskEntry{
				{Key: "/", Total: ptrInt64(500 * 1024 * 1024 * 1024)},
			},
		},
	}

	cache.UpdateFromMetrics(nodeID, m1, nil, nil)

	latest, ok := cache.Get(nodeID)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if latest.CpuPct != cpu || latest.MemUsed != memUsed || latest.NetUpBps != upBps {
		t.Fatalf("unexpected latest values: %+v", latest)
	}
	if latest.DiskUsed == nil || *latest.DiskUsed != diskUsed {
		t.Fatalf("expected DiskUsed=%d, got %v", diskUsed, latest.DiskUsed)
	}
	if latest.MemTotal == nil || *latest.MemTotal != 16*1024*1024*1024 {
		t.Fatalf("expected MemTotal preserved, got %v", latest.MemTotal)
	}
	if notifyCalls != 1 {
		t.Fatalf("expected 1 notify call, got %d", notifyCalls)
	}
	if notified.CpuPct != cpu {
		t.Fatalf("expected notified.CpuPct=%f, got %f", cpu, notified.CpuPct)
	}

	// 4. 第二轮上报 (仅 Fast 档，无 Slow 字段)
	cpu2 := 25.0
	memUsed2 := int64(5 * 1024 * 1024 * 1024)
	m2 := &protocol.MetricsParams{
		TsMs:    1700000005000,
		CPUPct:  &cpu2,
		MemUsed: &memUsed2,
	}

	cache.UpdateFromMetrics(nodeID, m2, nil, nil)

	latest2, _ := cache.Get(nodeID)
	if latest2.CpuPct != cpu2 || latest2.MemUsed != memUsed2 {
		t.Fatalf("expected updated cpu and mem, got %+v", latest2)
	}
	// 确认上一轮的 Slow 字段依然得以保留，没有被清空为 nil
	if latest2.DiskUsed == nil || *latest2.DiskUsed != diskUsed {
		t.Fatalf("expected DiskUsed preserved from previous slow round, got %v", latest2.DiskUsed)
	}
	if latest2.TcpCount == nil || *latest2.TcpCount != int(tcpCnt) {
		t.Fatalf("expected TcpCount preserved from previous slow round, got %v", latest2.TcpCount)
	}
	if latest2.MemTotal == nil || *latest2.MemTotal != 16*1024*1024*1024 {
		t.Fatalf("expected MemTotal preserved, got %v", latest2.MemTotal)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
