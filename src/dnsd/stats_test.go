package dnsd

import (
	"fmt"
	"testing"
	"time"
)

func TestStats_IncrementAndSnapshot(t *testing.T) {
	s := NewGlobalStats()
	s.Increment(true, "ads.com", "Browser")
	s.Increment(false, "good.com", "Browser")
	s.Increment(true, "ads.com", "App")

	snap := s.Snapshot()
	if snap.TotalQueries != 3 || snap.BlockedQueries != 2 {
		t.Fatalf("expected 3 total, 2 blocked, got %d, %d", snap.TotalQueries, snap.BlockedQueries)
	}
	if snap.BlockPercent < (2.0/3.0*100.0)-0.001 || snap.BlockPercent > (2.0/3.0*100.0)+0.001 {
		t.Errorf("wrong block percent: %v", snap.BlockPercent)
	}
	if snap.TopDomains["ads.com"] != 2 {
		t.Errorf("expected ads.com to have 2 blocks, got %d", snap.TopDomains["ads.com"])
	}
}

func TestStats_MaxCapacity(t *testing.T) {
	s := NewGlobalStats()
	for i := 0; i < 10005; i++ {
		s.Increment(true, fmt.Sprintf("domain%d.com", i), fmt.Sprintf("app%d", i))
	}

	snap := s.Snapshot()
	if snap.BlockedQueries != 10005 {
		t.Fatalf("expected 10005 blocked, got %d", snap.BlockedQueries)
	}

	s.mu.Lock()
	now := time.Now()
	hour := now.Truncate(time.Hour).Unix()
	b := s.buckets[hour]
	lDomain := len(b.domains)
	lApp := len(b.apps)
	s.mu.Unlock()

	if lDomain != 10000 {
		t.Errorf("expected 10000 top domains, got %d", lDomain)
	}
	if lApp != 10000 {
		t.Errorf("expected 10000 top apps, got %d", lApp)
	}
}
