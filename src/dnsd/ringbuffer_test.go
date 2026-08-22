// src/dnsd/ringbuffer_test.go
package dnsd

import (
	"testing"
)

func TestRingBuffer_PushAndSnapshot(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(QueryRecord{Domain: "1.com"})
	rb.Push(QueryRecord{Domain: "2.com"})
	rb.Push(QueryRecord{Domain: "3.com"})
	rb.Push(QueryRecord{Domain: "4.com"})

	snap := rb.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(snap))
	}
	if snap[0].Domain != "2.com" {
		t.Errorf("expected first entry to be 2.com, got %s", snap[0].Domain)
	}
	if snap[2].Domain != "4.com" {
		t.Errorf("expected last entry to be 4.com, got %s", snap[2].Domain)
	}
}
