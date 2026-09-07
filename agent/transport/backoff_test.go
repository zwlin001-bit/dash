package transport

import (
	"math/rand"
	"testing"
	"time"
)

func TestBackoffSequenceAndJitter(t *testing.T) {
	b := NewBackoff()

	expectedBases := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second,
		60 * time.Second, // 封顶
		60 * time.Second, // 再次封顶
	}

	for i, base := range expectedBases {
		stepBefore := b.CurrentStep()
		d := b.Next()

		minAllowed := time.Duration(float64(base) * 0.8)
		maxAllowed := time.Duration(float64(base) * 1.2)

		if d < minAllowed || d > maxAllowed {
			t.Fatalf("step %d (base %v): got %v, expected in [%v, %v]", i, base, d, minAllowed, maxAllowed)
		}

		if i < len(DefaultBackoffSequence)-1 {
			if b.CurrentStep() != stepBefore+1 {
				t.Fatalf("expected step to increment from %d to %d, got %d", stepBefore, stepBefore+1, b.CurrentStep())
			}
		} else {
			if b.CurrentStep() != len(DefaultBackoffSequence)-1 {
				t.Fatalf("expected step capped at %d, got %d", len(DefaultBackoffSequence)-1, b.CurrentStep())
			}
		}
	}
}

func TestBackoffJitterFluctuation(t *testing.T) {
	// 验证退避间隔具备随机抖动（不是固定不变的值）
	b := NewBackoff(10 * time.Second)
	seen := make(map[time.Duration]bool)

	for i := 0; i < 20; i++ {
		d := b.Next()
		seen[d] = true
	}

	if len(seen) < 15 {
		t.Fatalf("expected high variance in jittered delays, got %d unique values out of 20", len(seen))
	}
}

func TestBackoffReset(t *testing.T) {
	b := NewBackoff()

	// 步进几步
	for i := 0; i < 4; i++ {
		_ = b.Next()
	}

	if b.CurrentStep() != 4 {
		t.Fatalf("expected step 4, got %d", b.CurrentStep())
	}

	b.Reset()

	if b.CurrentStep() != 0 {
		t.Fatalf("expected step 0 after reset, got %d", b.CurrentStep())
	}

	// 重置后第一步基准应当是 1s (0.8s ~ 1.2s)
	d := b.Next()
	if d < 800*time.Millisecond || d > 1200*time.Millisecond {
		t.Fatalf("got %v after reset, expected within [800ms, 1200ms]", d)
	}
}

func TestBackoffCustomRNG(t *testing.T) {
	// 固定种子，验证计算确定性
	rng1 := rand.New(rand.NewSource(12345))
	b1 := NewCustomBackoff([]time.Duration{5 * time.Second}, rng1)

	rng2 := rand.New(rand.NewSource(12345))
	b2 := NewCustomBackoff([]time.Duration{5 * time.Second}, rng2)

	d1 := b1.Next()
	d2 := b2.Next()

	if d1 != d2 {
		t.Fatalf("expected identical values with same seed: d1=%v, d2=%v", d1, d2)
	}
}
