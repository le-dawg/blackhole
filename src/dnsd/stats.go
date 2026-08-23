package dnsd

import (
	"sort"
	"sync"
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

type bucket struct {
	total   uint64
	blocked uint64
	domains map[string]uint64
	apps    map[string]uint64
}

func newBucket() *bucket {
	return &bucket{
		domains: make(map[string]uint64),
		apps:    make(map[string]uint64),
	}
}

type GlobalStats struct {
	mu      sync.Mutex
	buckets map[int64]*bucket
}

func NewGlobalStats() *GlobalStats {
	return &GlobalStats{
		buckets: make(map[int64]*bucket),
	}
}

func (s *GlobalStats) Increment(blocked bool, domain, app string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	hour := now.Truncate(time.Hour).Unix()
	
	b, ok := s.buckets[hour]
	if !ok {
		b = newBucket()
		s.buckets[hour] = b
	}

	b.total++
	if blocked {
		b.blocked++
		if _, exists := b.domains[domain]; exists || len(b.domains) < 10000 {
			b.domains[domain]++
		}
		if _, exists := b.apps[app]; exists || len(b.apps) < 10000 {
			b.apps[app]++
		}
	}
	
	cutoff := now.Add(-24 * time.Hour).Truncate(time.Hour).Unix()
	for h := range s.buckets {
		if h < cutoff {
			delete(s.buckets, h)
		}
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
	s.mu.Lock()
	defer s.mu.Unlock()

	var total, blocked uint64
	domains := make(map[string]uint64)
	apps := make(map[string]uint64)

	now := time.Now()
	cutoff := now.Add(-24 * time.Hour).Truncate(time.Hour).Unix()

	for h, b := range s.buckets {
		if h < cutoff {
			continue
		}
		total += b.total
		blocked += b.blocked
		for k, v := range b.domains {
			domains[k] += v
		}
		for k, v := range b.apps {
			apps[k] += v
		}
	}

	pct := 0.0
	if total > 0 {
		pct = float64(blocked) / float64(total) * 100.0
	}

	return StatsSnapshot{
		TotalQueries:   total,
		BlockedQueries: blocked,
		BlockPercent:   pct,
		TopDomains:     getTop5(domains),
		TopApps:        getTop5(apps),
		WindowStart:    now.Add(-24 * time.Hour),
	}
}
