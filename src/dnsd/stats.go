package dnsd

import (
	"sort"
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
		
		if _, exists := s.topDomains[domain]; exists || len(s.topDomains) < 10000 {
			s.topDomains[domain]++
		}
		
		if _, exists := s.topApps[app]; exists || len(s.topApps) < 10000 {
			s.topApps[app]++
		}
		
		s.mu.Unlock()
	}
}

type kv struct {
	k string
	v uint64
}

func getTop5(m map[string]uint64) map[string]uint64 {
	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}

	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].v == sorted[j].v {
			return sorted[i].k < sorted[j].k
		}
		return sorted[i].v > sorted[j].v
	})

	res := make(map[string]uint64)
	for i := 0; i < 5 && i < len(sorted); i++ {
		res[sorted[i].k] = sorted[i].v
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
	tdCopy := make(map[string]uint64, len(s.topDomains))
	for k, v := range s.topDomains {
		tdCopy[k] = v
	}
	
	taCopy := make(map[string]uint64, len(s.topApps))
	for k, v := range s.topApps {
		taCopy[k] = v
	}
	w := s.window
	s.mu.Unlock()

	td := getTop5(tdCopy)
	ta := getTop5(taCopy)

	return StatsSnapshot{
		TotalQueries:   t,
		BlockedQueries: b,
		BlockPercent:   pct,
		TopDomains:     td,
		TopApps:        ta,
		WindowStart:    w,
	}
}
