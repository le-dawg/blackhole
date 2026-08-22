# Spec C — IPC & Stats Engine

## Goal
Add a structured in-memory query log and a lightweight Unix domain socket server inside the
Go daemon so the SwiftUI client can read live statistics and send control commands without
polling the filesystem or parsing log files.

## Scope
Items from Codex audit: 5.1, 5.2, 5.3

## Global Constraints
- Platform: macOS 15+
- Ring buffer: last 1,000 entries, zero heap reallocation after init
- IPC socket path: `/var/run/blackhole.sock`
- IPC protocol: newline-delimited JSON (one JSON object per line)
- IPC must be readable by unprivileged user processes (socket permissions: `0666`)
- Stats engine must survive transient SwiftUI restarts (data lives in daemon, not client)

---

## Components

### 1. Query Ring Buffer (`src/dnsd/ringbuffer.go`)
A fixed-size circular buffer storing the last 1,000 query records. Allocated once at startup
and never reallocated.

**Entry schema:**
```go
type QueryRecord struct {
    Timestamp  time.Time
    Domain     string
    QueryType  uint16   // dns.TypeA, dns.TypeAAAA, etc.
    Status     string   // "BLOCKED", "ALLOWED", "EXCLUDED", "CACHED"
    ProcessName string
    BundleID   string
    LatencyMs  float64
}
```

**Behavior:**
- Thread-safe via `sync.RWMutex`.
- `Push(r QueryRecord)` overwrites the oldest entry when full.
- `Snapshot() []QueryRecord` returns a copy of all entries in chronological order for
  safe reading by IPC handlers without holding the lock.
- Every query path in `src/main.go` pushes a record after determining status.

### 2. 24-Hour Aggregate Stats (`src/dnsd/stats.go`)
Maintains rolling 24-hour counters updated atomically after every query.

**Tracked values:**
```go
type Stats struct {
    TotalQueries   uint64
    BlockedQueries uint64
    // Top-5 blocked domains: map[string]uint64, pruned to top 5 on read
    // Top-5 requesting apps: map[string]uint64, pruned to top 5 on read
    WindowStart    time.Time  // reset every 24 hours
}
```

- Counters use `sync/atomic` for lock-free increment on the hot path.
- Top-5 maps are protected by a separate `sync.Mutex` (updated only on block/exclusion events,
  not every query).
- `BlockPercent() float64` is computed on read: `BlockedQueries / TotalQueries * 100`.
- At midnight (or 24h after daemon start), counters reset and `WindowStart` advances.

### 3. Unix Domain Socket IPC Server (`src/dnsd/ipc_server.go`)
A lightweight HTTP-over-Unix-socket server using Go's standard `net/http` package mounted
on a `net.Listen("unix", "/var/run/blackhole.sock")` listener.

**Endpoints:**

| Method | Path | Response |
|--------|------|----------|
| GET | `/stats` | JSON: `{ total, blocked, blockPercent, topDomains[], topApps[], windowStart }` |
| GET | `/queries` | JSON array of last N `QueryRecord` entries (default N=100, max 1000 via `?limit=N`) |
| POST | `/pause` | Body: `{ "durationSeconds": 300 }`. Daemon sets a pause flag; returns `{ "ok": true }` |
| POST | `/reload` | Forces immediate blocklist re-download. Returns `{ "ok": true }` |

**Security:**
- Socket file permissions set to `0666` so the unprivileged SwiftUI app can connect.
- No authentication: access is local-only (Unix socket cannot be accessed remotely).
- `/pause` and `/reload` commands are rate-limited to 1 request per 5 seconds.

**Pause behavior:**
- When paused, the query handler skips all blocklist and exclusion checks and forwards every
  query directly to upstream.
- A `time.AfterFunc` goroutine automatically clears the pause flag after the requested
  duration.
- Pausing while already paused resets the timer.

---

## Data Flow

```
[Query handler in main.go]
        ↓ after every query
  ringbuffer.Push(QueryRecord)
  stats.Increment(status)
        ↓
[IPC server goroutine] ←── SwiftUI client connects via /var/run/blackhole.sock
        ↓
  GET /stats → stats.Snapshot()
  GET /queries → ringbuffer.Snapshot()
  POST /pause → set pauseFlag + timer
  POST /reload → trigger gravity.Refresh()
```

---

## Testing Requirements
- Unit test: ring buffer wraps correctly at capacity (entry 1001 overwrites entry 1)
- Unit test: `Snapshot()` returns entries in chronological order under concurrent writes
- Unit test: `BlockPercent()` returns 0 when `TotalQueries` is 0 (no divide-by-zero)
- Unit test: stats reset to 0 after 24-hour window elapses
- Unit test: `/pause` endpoint sets pause flag and clears it after duration
- Unit test: `/pause` rate-limiter rejects second call within 5 seconds
- Integration test: SwiftUI-equivalent Go client connects to socket, calls `/stats`, and
  receives valid JSON with correct field types

---

## Files Created / Modified
| Action | Path |
|--------|------|
| Create | `src/dnsd/ringbuffer.go` |
| Create | `src/dnsd/ringbuffer_test.go` |
| Create | `src/dnsd/stats.go` |
| Create | `src/dnsd/stats_test.go` |
| Create | `src/dnsd/ipc_server.go` |
| Create | `src/dnsd/ipc_server_test.go` |
| Modify | `src/main.go` (start IPC server goroutine; push QueryRecord after each query) |
