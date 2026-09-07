package ingest

import (
	"context"
	"log"
	"sync"
	"time"
)

// Scheduler coordinates timed execution of 1m, 1h, 1d rollups and retention cleanup.
// Adheres strictly to docs/agy/tasks/P1-12-rollup与保留期.md:
// - 1m: every minute at second 10 -> window [previous minute start, previous minute end)
// - 1h: every hour at minute 2   -> window [previous hour start, previous hour end)
// - 1d: daily at 00:05 UTC       -> window [yesterday 00:00, today 00:00)
// - Retention: every hour
// - Catch-up on startup: catches up missing rollups from MAX(bucket_ms) in sample_host_1m.
type Scheduler struct {
	rollup    *RollupService
	retention *RetentionService
	stopCh    chan struct{}
	doneWg    sync.WaitGroup
	mu        sync.Mutex
	running   bool
}

// NewScheduler constructs a new Scheduler instance.
func NewScheduler(rollup *RollupService, retention *RetentionService) *Scheduler {
	return &Scheduler{
		rollup:    rollup,
		retention: retention,
		stopCh:    make(chan struct{}),
	}
}

// Start initiates the scheduler:
// 1. Performs startup catch-up of missing rollups and initial retention cleanup.
// 2. Starts background loops for 1m, 1h, 1d rollups and hourly retention.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// 1. Startup catch-up
	nowMs := time.Now().UnixMilli()
	if err := s.rollup.CatchUp(ctx, nowMs); err != nil {
		log.Printf("[ingest/scheduler] startup catch-up warning: %v", err)
	}
	if err := s.retention.Clean(ctx, nowMs); err != nil {
		log.Printf("[ingest/scheduler] startup retention cleanup warning: %v", err)
	}

	// 2. Launch background schedulers
	s.doneWg.Add(4)
	go s.run1mLoop(ctx)
	go s.run1hLoop(ctx)
	go s.run1dLoop(ctx)
	go s.runRetentionLoop(ctx)

	return nil
}

// Stop gracefully terminates the scheduler loops.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()

	s.doneWg.Wait()
}

func (s *Scheduler) run1mLoop(ctx context.Context) {
	defer s.doneWg.Done()

	for {
		now := time.Now()
		next := Next1mTime(now)
		delay := next.Sub(now)

		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case triggerTime := <-time.After(delay):
			// Process [previous minute start, previous minute end)
			curMinute := triggerTime.Truncate(time.Minute)
			fromMs := curMinute.Add(-1 * time.Minute).UnixMilli()
			toMs := curMinute.UnixMilli()

			if err := s.rollup.Rollup1m(ctx, fromMs, toMs); err != nil {
				log.Printf("[ingest/scheduler] rollup 1m [%d, %d) failed: %v", fromMs, toMs, err)
			}
		}
	}
}

func (s *Scheduler) run1hLoop(ctx context.Context) {
	defer s.doneWg.Done()

	for {
		now := time.Now()
		next := Next1hTime(now)
		delay := next.Sub(now)

		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case triggerTime := <-time.After(delay):
			// Process [previous hour start, previous hour end)
			curHour := triggerTime.Truncate(time.Hour)
			fromMs := curHour.Add(-1 * time.Hour).UnixMilli()
			toMs := curHour.UnixMilli()

			if err := s.rollup.Rollup1h(ctx, fromMs, toMs); err != nil {
				log.Printf("[ingest/scheduler] rollup 1h [%d, %d) failed: %v", fromMs, toMs, err)
			}
		}
	}
}

func (s *Scheduler) run1dLoop(ctx context.Context) {
	defer s.doneWg.Done()

	for {
		now := time.Now()
		next := Next1dTime(now)
		delay := next.Sub(now)

		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case triggerTime := <-time.After(delay):
			// Process [yesterday 00:00, today 00:00) UTC
			utc := triggerTime.UTC()
			today00 := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
			fromMs := today00.AddDate(0, 0, -1).UnixMilli()
			toMs := today00.UnixMilli()

			if err := s.rollup.Rollup1d(ctx, fromMs, toMs); err != nil {
				log.Printf("[ingest/scheduler] rollup 1d [%d, %d) failed: %v", fromMs, toMs, err)
			}
		}
	}
}

func (s *Scheduler) runRetentionLoop(ctx context.Context) {
	defer s.doneWg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if err := s.retention.Clean(ctx, t.UnixMilli()); err != nil {
				log.Printf("[ingest/scheduler] retention clean failed: %v", err)
			}
		}
	}
}

// Next1mTime returns the next instant where second == 10.
func Next1mTime(t time.Time) time.Time {
	next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 10, 0, t.Location())
	if !next.After(t) {
		next = next.Add(time.Minute)
	}
	return next
}

// Next1hTime returns the next instant where minute == 2, second == 0.
func Next1hTime(t time.Time) time.Time {
	next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 2, 0, 0, t.Location())
	if !next.After(t) {
		next = next.Add(time.Hour)
	}
	return next
}

// Next1dTime returns the next instant where UTC time is 00:05:00.
func Next1dTime(t time.Time) time.Time {
	utc := t.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 5, 0, 0, time.UTC)
	if !next.After(utc) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
