package ingest_test

import (
	"context"
	"testing"
	"time"

	"dash/internal/ingest"
)

func TestSchedulerTimingCalculations(t *testing.T) {
	// 1. Next1mTime: must align to next second 10
	now := time.Date(2026, 9, 7, 12, 30, 5, 0, time.UTC)
	next1m := ingest.Next1mTime(now)
	if next1m.Minute() != 30 || next1m.Second() != 10 {
		t.Fatalf("expected 12:30:10, got %v", next1m)
	}

	// When current second is past 10: advances to next minute second 10
	nowPast := time.Date(2026, 9, 7, 12, 30, 25, 0, time.UTC)
	next1mPast := ingest.Next1mTime(nowPast)
	if next1mPast.Minute() != 31 || next1mPast.Second() != 10 {
		t.Fatalf("expected 12:31:10, got %v", next1mPast)
	}

	// 2. Next1hTime: must align to next minute 2, second 0
	nowHour := time.Date(2026, 9, 7, 12, 1, 0, 0, time.UTC)
	next1h := ingest.Next1hTime(nowHour)
	if next1h.Hour() != 12 || next1h.Minute() != 2 || next1h.Second() != 0 {
		t.Fatalf("expected 12:02:00, got %v", next1h)
	}

	nowHourPast := time.Date(2026, 9, 7, 12, 15, 0, 0, time.UTC)
	next1hPast := ingest.Next1hTime(nowHourPast)
	if next1hPast.Hour() != 13 || next1hPast.Minute() != 2 || next1hPast.Second() != 0 {
		t.Fatalf("expected 13:02:00, got %v", next1hPast)
	}

	// 3. Next1dTime: must align to next day 00:05:00 UTC
	nowDay := time.Date(2026, 9, 7, 0, 1, 0, 0, time.UTC)
	next1d := ingest.Next1dTime(nowDay)
	if next1d.Day() != 7 || next1d.Hour() != 0 || next1d.Minute() != 5 || next1d.Second() != 0 {
		t.Fatalf("expected 2026-09-07 00:05:00, got %v", next1d)
	}

	nowDayPast := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	next1dPast := ingest.Next1dTime(nowDayPast)
	if next1dPast.Day() != 8 || next1dPast.Hour() != 0 || next1dPast.Minute() != 5 || next1dPast.Second() != 0 {
		t.Fatalf("expected 2026-09-08 00:05:00, got %v", next1dPast)
	}
}

func TestSchedulerLifecycle(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rollup := ingest.NewRollupService(d)
	retention := ingest.NewRetentionService(d)
	sched := ingest.NewScheduler(rollup, retention)

	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify idempotency of start
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("repeat Start failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	sched.Stop()
	// Verify repeat stop does not panic
	sched.Stop()
}
