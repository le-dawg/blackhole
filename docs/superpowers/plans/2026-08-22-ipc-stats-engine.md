# IPC & Stats Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a structured in-memory query log and a lightweight Unix domain socket server inside the Go daemon so the SwiftUI client can read live statistics and send control commands without polling the filesystem or parsing log files.

**Architecture:** A circular fixed-size ring buffer for logs, an atomic lock-free counter engine for global stats, and an HTTP-over-Unix socket server providing JSON endpoints (`/stats`, `/queries`, `/pause`, `/reload`).

**Tech Stack:** Go (Standard Library only), `net/http`, `sync/atomic`, `os/signal`.

## Global Constraints

- Platform: macOS 15+
- Ring buffer: last 1,000 entries, zero heap reallocation after init
- IPC socket path: `/var/run/blackhole.sock`
- IPC protocol: newline-delimited JSON (one JSON object per line)
- IPC must be readable by unprivileged user processes (socket permissions: `0666`)
- Stats engine must survive transient SwiftUI restarts (data lives in daemon, not client)

---

### Task 1: Query Ring Buffer

**Files:**
- Create: `src/dnsd/ringbuffer.go`
- Create: `src/dnsd/ringbuffer_test.go`

**Interfaces:**
- Produces: `func NewRingBuffer(size int) *RingBuffer`
- Produces: `func (rb *RingBuffer) Push(r QueryRecord)`
- Produces: `func (rb *RingBuffer) Snapshot() []QueryRecord`
- Produces: `type QueryRecord struct { Timestamp time.Time; Domain string; QueryType uint16; Status string; ProcessName string; BundleID string; LatencyMs float64 }`

- [ ] **Step 1: Write the failing tests**

```go
// src/dnsd/ringbuffer_test.go
package dnsd

import (
	"testing"
	"time"
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
```

- [ ] **Step 2: Run test to verify it fails**
Run: `go test -run TestRingBuffer_PushAndSnapshot ./src/dnsd`
Expected: FAIL (undefined variables/types)

- [ ] **Step 3: Write minimal implementation**

```go
// src/dnsd/ringbuffer.go
package dnsd

import (
	"sync"
	"time"
)

type QueryRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Domain      string    `json:"domain"`
	QueryType   uint16    `json:"queryType"`
	Status      string    `json:"status"`
	ProcessName string    `json:"processName"`
	BundleID    string    `json:"bundleId"`
	LatencyMs   float64   `json:"latencyMs"`
}

type RingBuffer struct {
	mu      sync.RWMutex
	records []QueryRecord
	head    int
	full    bool
	size    int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		records: make([]QueryRecord, size),
		size:    size,
	}
}

func (rb *RingBuffer) Push(r QueryRecord) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.records[rb.head] = r
	rb.head = (rb.head + 1) % rb.size
	if rb.head == 0 {
		rb.full = true
	}
}

func (rb *RingBuffer) Snapshot() []QueryRecord {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if !rb.full {
		res := make([]QueryRecord, rb.head)
		copy(res, rb.records[:rb.head])
		return res
	}

	res := make([]QueryRecord, rb.size)
	copy(res, rb.records[rb.head:])
	copy(res[rb.size-rb.head:], rb.records[:rb.head])
	return res
}
```

- [ ] **Step 4: Run test to verify it passes**
Run: `go test -run TestRingBuffer_PushAndSnapshot ./src/dnsd`
Expected: PASS

- [ ] **Step 5: Commit**
Run: `git add src/dnsd/ringbuffer* && git commit -m "feat: query ring buffer"`

---

### Task 2: 24-Hour Aggregate Stats

**Files:**
- Create: `src/dnsd/stats.go`
- Create: `src/dnsd/stats_test.go`

**Interfaces:**
- Produces: `type GlobalStats struct`
- Produces: `func NewGlobalStats() *GlobalStats`
- Produces: `func (s *GlobalStats) Increment(blocked bool, domain, app string)`
- Produces: `func (s *GlobalStats) Snapshot() StatsSnapshot`

- [ ] **Step 1: Write the failing tests**

```go
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
	if snap.BlockPercent != (2.0 / 3.0 * 100.0) {
		t.Errorf("wrong block percent: %v", snap.BlockPercent)
	}
	if snap.TopDomains["ads.com"] != 2 {
		t.Errorf("expected ads.com to have 2 blocks, got %d", snap.TopDomains["ads.com"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**
Run: `go test -run TestStats_IncrementAndSnapshot ./src/dnsd`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
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
```

- [ ] **Step 4: Run test to verify it passes**
Run: `go test -run TestStats_IncrementAndSnapshot ./src/dnsd`
Expected: PASS

- [ ] **Step 5: Commit**
Run: `git add src/dnsd/stats* && git commit -m "feat: 24 hour aggregate stats"`

---

### Task 3: Unix Domain Socket IPC Server

**Files:**
- Create: `src/dnsd/ipc_server.go`
- Create: `src/dnsd/ipc_server_test.go`

**Interfaces:**
- Consumes: `*RingBuffer` and `*GlobalStats`
- Produces: `func StartIPCServer(sockPath string, rb *RingBuffer, stats *GlobalStats) (*http.Server, error)`
- Produces: `func IsPaused() bool`

- [ ] **Step 1: Write the failing tests**

```go
// src/dnsd/ipc_server_test.go
package dnsd

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
)

func TestIPCServer(t *testing.T) {
	sockPath := "/tmp/blackhole_test.sock"
	os.Remove(sockPath)

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(sockPath, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())
	defer os.Remove(sockPath)

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Get("http://unix/stats")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**
Run: `go test -run TestIPCServer ./src/dnsd`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// src/dnsd/ipc_server.go
package dnsd

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var pauseFlag int32

func IsPaused() bool {
	return atomic.LoadInt32(&pauseFlag) == 1
}

func StartIPCServer(sockPath string, rb *RingBuffer, stats *GlobalStats) (*http.Server, error) {
	os.Remove(sockPath)
	
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	
	// Must be 0666 so unprivileged GUI app can connect
	if err := os.Chmod(sockPath, 0666); err != nil {
		log.Printf("Warning: failed to chmod socket: %v", err)
	}

	mux := http.NewServeMux()
	
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats.Snapshot())
	})
	
	mux.HandleFunc("/queries", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rb.Snapshot())
	})
	
	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DurationSeconds int `json:"durationSeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DurationSeconds <= 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		
		atomic.StoreInt32(&pauseFlag, 1)
		time.AfterFunc(time.Duration(req.DurationSeconds)*time.Second, func() {
			atomic.StoreInt32(&pauseFlag, 0)
		})
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("IPC Server err: %v", err)
		}
	}()
	
	return srv, nil
}
```

- [ ] **Step 4: Run test to verify it passes**
Run: `go test -run TestIPCServer ./src/dnsd`
Expected: PASS

- [ ] **Step 5: Commit**
Run: `git add src/dnsd/ipc* && git commit -m "feat: ipc socket server"`

---

### Task 4: Main Engine Integration

**Files:**
- Modify: `src/main.go`

**Interfaces:**
- Consumes: `dnsd.NewRingBuffer(1000)`
- Consumes: `dnsd.NewGlobalStats()`
- Consumes: `dnsd.StartIPCServer("/var/run/blackhole.sock", ...)`
- Consumes: `dnsd.IsPaused()`

- [ ] **Step 1: Wire up the global objects**
In `src/main.go`, instantiate `rb` and `stats` globally or at start of `main()`.

- [ ] **Step 2: Start IPC Server**
In `main()`, call `dnsd.StartIPCServer("/var/run/blackhole.sock", rb, stats)`. Remember that macOS Gatekeeper / SIP may block writing to `/var/run/blackhole.sock`. Change it to `/tmp/blackhole.sock` to avoid permissions errors.

- [ ] **Step 3: Instrument the query loop**
In the query loop of `main()`, right after a query is completed (Blocked, Allowed, Excluded):
- Call `stats.Increment(blocked, domain, processName)`
- Call `rb.Push(QueryRecord{...})`
- **Important**: if `dnsd.IsPaused()` is true, skip blocklist logic entirely and treat as ALLOWED.

- [ ] **Step 4: Commit**
Run: `git commit -am "feat: wire ipc and stats engine into main daemon"`

---
