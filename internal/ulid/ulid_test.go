package ulid

import (
	"strings"
	"testing"
	"time"
)

func TestULID(t *testing.T) {
	id := New()
	if len(id) != 26 {
		t.Fatalf("expected length 26, got %d (%s)", len(id), id)
	}

	if !IsValid(id) {
		t.Fatalf("expected valid ULID, got %s", id)
	}

	// Test Invalid cases
	if IsValid("too-short") {
		t.Fatal("expected false for short string")
	}
	if IsValid(id + "1") {
		t.Fatal("expected false for long string")
	}
	// 'I', 'L', 'O', 'U' are invalid in Crockford Base32
	if IsValid(strings.Replace(id, id[10:11], "I", 1)) {
		t.Fatal("expected false for string containing 'I'")
	}

	// Test monotonicity / time sorting
	t1 := time.UnixMilli(1000000000000)
	t2 := time.UnixMilli(2000000000000)
	id1 := NewWithTime(t1)
	id2 := NewWithTime(t2)
	if id1 >= id2 {
		t.Fatalf("expected id1 < id2, got %s >= %s", id1, id2)
	}

	// Test uniqueness across 5000 IDs
	seen := make(map[string]bool, 5000)
	for i := 0; i < 5000; i++ {
		uid := New()
		if seen[uid] {
			t.Fatalf("duplicate ULID generated: %s", uid)
		}
		seen[uid] = true
	}
}
