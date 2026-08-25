# Blackhole Architecture & Systems Design

Blackhole is a high-performance, low-latency, zero-allocation local DNS sinkhole daemon for macOS built in Go and Cgo. It provides network-level advertisement, tracking, and malware blocking with application-level exclusion routing and dynamic VPN awareness.

---

## 1. System Overview & Component Topology

```
+-------------------------------------------------------------------------+
|                              macOS Client                               |
| (Browsers, Native Apps, Background Daemons, VPN Clients, CLI Utilities) |
+-------------------------------------------------------------------------+
                                   |
                                   | UDP Query (Port 53 / Custom Port)
                                   v
+-------------------------------------------------------------------------+
|                              Daemon Engine                              |
|                                                                         |
|  +-------------------------------------------------------------------+  |
|  |                 Bounded Query Worker Pool (64 Workers)            |  |
|  |  [Query Queue: 2048 Slots] -> Fast SERVFAIL on Overload           |  |
|  +-------------------------------------------------------------------+  |
|                                  |                                      |
|                                  v                                      |
|  +-------------------------------------------------------------------+  |
|  |                     Security & Policy Evaluation                  |  |
|  |  1. Explicit User Blacklist (Always Wins, Sinkhole 0.0.0.0 / ::)  |  |
|  |  2. Pause Service Verification (Temporary Global Bypass)          |  |
|  |  3. App Exclusion Inspection (proc_pidpath / LOCAL_PEEREPID)     |  |
|  |  4. Core FilterEngine (User Whitelist -> Allowlist -> Radix Trie) |  |
|  |  5. Extension FilterChain (`Filter.Process`)                      |  |
|  +-------------------------------------------------------------------+  |
|                                  |                                      |
|            +---------------------+---------------------+                |
|            | (Blocked / Sinkhole)                      | (Allowed)      |
|            v                                           v                |
|   [Synthesized 0.0.0.0]                     +------------------------+  |
|                                             |  LRU DNS Cache (4000)  |  |
|                                             +------------------------+  |
|                                                        | (Cache Miss)   |
|                                                        v                |
|                                             +------------------------+  |
|                                             | Upstream RaceForwarder |  |
|                                             | (1.1.1.1, 8.8.8.8,     |  |
|                                             |  Dynamic VPN Resolvers)|  |
|                                             +------------------------+  |
|                                                        |                |
|                                                        v                |
|                                             +------------------------+  |
|                                             | Response Bailiwick     |  |
|                                             | & CNAME Validation     |  |
|                                             +------------------------+  |
+-------------------------------------------------------------------------+
```

---

## 2. Concurrency & Memory Model

### 2.1 Zero-Allocation Radix Trie Resolution
Domain filtering in the hot path is executed against an in-memory reversed-label Radix Trie (`FilterEngine`).
- **Data Structure:** Each node represents a domain label (e.g., `["com", "doubleclick", "ad"]`).
- **Atomic Pointer Swapping:** Read operations execute lock-free via `atomic.Value` pointing to immutable `engineState`. Lookups perform zero heap allocations (`0 B/op`, `0 allocs/op`, ~56 ns/op).
- **Copy-on-Write Mutations:** Ad-hoc additions (`AddBlockedDomain`) clone the existing trie under `updateMu` and atomically swap the active root.

### 2.2 Bounded Worker Pool & Graceful Drain
- Incoming UDP packets are received by `runMessageLoop` and dispatched to a buffered `queryQueue` (capacity 2048).
- A fixed pool of 64 worker goroutines processes DNS lookups concurrently.
- If the queue is saturated, the server emits a lightweight, fast SERVFAIL response and records the query in telemetry rather than allocating unbound goroutines.
- During daemon termination, `ctx.Done()` signals workers, stops ingestion by setting a read deadline, closes `queryQueue`, waits for workers to drain with a 3-second timeout, and closes the UDP listener cleanly.

---

## 3. Security & Trust Boundaries

### 3.1 IPC Socket Authentication
The REST IPC API operates exclusively over a Unix domain socket (`/var/run/blackhole.sock`).
- **`LOCAL_PEERCRED` Socket Verification:** The custom `AuthenticatedUnixListener` inspects peer credentials at the kernel syscall layer upon connection acceptance, rejecting unauthorized UIDs before HTTP framing is read.
- **Strict Payload Decoding:** JSON endpoints enforce single-document boundaries, rejecting trailing garbage or pipelined commands.

### 3.2 DNS Response Validation & Bailiwick Defense
To prevent cache poisoning and spoofing attacks:
- **Header Verification:** Responses must have `Header.Response == true`, matching OpCode, matching Query ID, and matching Question tuple (`QName`, `QType`, `QClass`).
- **Cardinality Limits:** Requests and responses must contain exactly 1 question, and the total resource record count across Answer, Authority, and Additional sections must not exceed 100.
- **CNAME Graph Traversal:** Answers are verified along an exact CNAME graph traversal starting from the queried domain (maximum 8 hops). Loop detection and conflicting CNAME owner checks immediately abort poisoned responses.
- **Auxiliary Section Sanitization:** Authority records must reside strictly within the queried zone's non-empty bailiwick. Additional records must match either the CNAME traversal path, validated authoritative NS glue records, or Answer MX/SRV targets. OPT records (RFC 6891) are preserved without corrupting Extended RCODE/flags in TTL fields.

---

## 4. List Parsing & Gravity Lifecycle

### 4.1 Transactional Two-Phase Commit & Crash Recovery
Gravity synchronization updates blocklists atomically without compromising live DNS resolution:
1. **Fetch & Pre-validation:** Feeds are fetched with conditional HTTP headers (`If-None-Match`, `If-Modified-Since`) and streamed through bounded readers (`maxBlocklistResponseBytes = 64 MiB`).
2. **Cryptographic Digesting:** Staged caches are hashed using SHA-256 (`GravityState.SHA256`).
3. **Drop Protection & Rule Floors:** Feeds must satisfy minimum rule floors and cannot collapse below 50% of the previous known-good rule count (calculated using ceiling division `(RuleCount + 1) / 2`).
4. **Crash Recovery Journal:** A transactional journal (`gravity.publish.json`) records state transitions before atomic renaming. If interrupted by power loss or crashes, the daemon reconciles the journal on startup, verifying cache SHA-256 digests before adopting new states or rolling back cleanly to the previous generation.
5. **Stale Source Eviction:** Removed list sources have their caches and state map entries safely purged.

### 4.2 Two-Pass AdGuard & Hosts Parsing
The `BlocklistParser` executes a two-pass algorithm:
- **Pass 1:** Spools block rules (`B`) and exception rules (`E`) to temporary storage while collecting `$badfilter` suppressors.
- **Pass 2:** Resolves bidirectional `$badfilter` overrides for both block rules and exception rules, ensuring rule order independence.

---

## 5. Extension Interfaces & Custom Filters

Blackhole exposes clean, thread-safe extension interfaces:

### `Filter`
```go
type Filter interface {
    Process(req []byte) (resp []byte, block bool, err error)
}
```
Global registration: `dnsd.RegisterFilter(f Filter)`. In the resolution pipeline, the core `FilterEngine` (evaluating User Whitelist, Gravity Allowlist, and Radix Trie Blocklist) executes as the first element of `FilterChain`, followed by registered extension `Filter` instances. Custom filters are invoked concurrently across worker goroutines and must be re-entrant and thread-safe. If a filter returns an error, the daemon immediately emits SERVFAIL to the client and records the event in telemetry.

### `ListParser` & `RuleAwareListParser`
```go
type ListParser interface {
    Parse(r io.Reader, onDomain func(string)) error
}

type RuleAwareListParser interface {
    ParseRules(r io.Reader, onBlock func(string), onException func(string)) error
}
```
Registration: `dnsd.RegisterParserForURL(urlPrefix string, p any)`. Validates non-nil interface implementation before registration.

---

## 6. Upstream Merging & Maintenance Path

To synchronize updates from upstream Pi-hole or threat feeds:
1. **Core Lists:** Update `DefaultLists` in `src/dnsd/gravity.go`.
2. **Custom Feeds:** Register dedicated parsers via `RegisterParserForURL`.
3. **Local Overrides:** Managed via `userlist.go` watching `whitelist.txt` and `blacklist.txt`.
