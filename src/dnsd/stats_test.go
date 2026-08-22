// src/dnsd/stats_test.go
package dnsd

import (
	"testing"
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
