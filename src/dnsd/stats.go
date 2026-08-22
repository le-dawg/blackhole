// src/dnsd/stats.go
package dnsd

import (
	"sync"
	"sync/atomic"
	"time"
)

type StatsSnapshot struct {
	TotalQueries   uint64            `json:"total"`
	BlockedQueries uint64            `json:"blocked"`
	BlockPercent   float64           `json:"blockPercent"`
	TopDomains     map[string]uint64 `json:"topDomains"`
	TopApps        map[string]uint64 `json:"topApps"`
	WindowStart    time.Time         `json:"windowStart"`
}

type GlobalStats struct {
	total   uint64
	blocked uint64
	window  time.Time
	
	mu         sync.Mutex
	topDomains map[string]uint64
	topApps    map[string]uint64
}

func NewGlobalStats() *GlobalStats {
	return &GlobalStats{
		window:     time.Now(),
		topDomains: make(map[string]uint64),
		topApps:    make(map[string]uint64),
	}
}

func (s *GlobalStats) Increment(blocked bool, domain, app string) {
	atomic.AddUint64(&s.total, 1)
	if blocked {
		atomic.AddUint64(&s.blocked, 1)
		
		s.mu.Lock()
		s.topDomains[domain]++
		s.topApps[app]++
		s.mu.Unlock()
	}
}

// Helper to get top 5 (naive approach for small maps)
func getTop5(m map[string]uint64) map[string]uint64 {
	res := make(map[string]uint64)
	for i := 0; i < 5; i++ {
		var maxKey string
		var maxVal uint64
		for k, v := range m {
			if v > maxVal && res[k] == 0 {
				maxKey = k
				maxVal = v
			}
		}
		if maxKey != "" {
			res[maxKey] = maxVal
		}
	}
	return res
}

func (s *GlobalStats) Snapshot() StatsSnapshot {
	t := atomic.LoadUint64(&s.total)
	b := atomic.LoadUint64(&s.blocked)
	pct := 0.0
	if t > 0 {
		pct = float64(b) / float64(t) * 100.0
	}
	
	s.mu.Lock()
	td := getTop5(s.topDomains)
	ta := getTop5(s.topApps)
	w := s.window
	s.mu.Unlock()

	return StatsSnapshot{
		TotalQueries:   t,
		BlockedQueries: b,
		BlockPercent:   pct,
		TopDomains:     td,
		TopApps:        ta,
		WindowStart:    w,
	}
}
