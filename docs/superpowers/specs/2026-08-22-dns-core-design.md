# Spec B — DNS Protocol Core

## Goal
Close all IPv6, HTTPS record, and DNS-over-HTTPS (DoH) leakage vectors; add an in-memory
response cache to eliminate redundant upstream round-trips; make upstream forwarding resilient
via race-forwarding; hook all active network interfaces simultaneously; and guarantee that
daemon shutdown always restores DNS to DHCP — preventing users from losing internet access.

## Scope
Items from Codex audit: 2.1, 2.2, 2.3, 2.4, 2.5, 4.1, 4.3

## Global Constraints
- Platform: macOS 15+
- DNS cache RAM budget: < 8 MB (bounded LRU)
- Cache lookup must return in < 0.2 ms
- Upstream race timeout: 500 ms before fallback
- Interface hooking applies to all services returned by `networksetup -listallnetworkservices`

---

## Components

### 1. Dual A + AAAA Block Response (`src/dnsd/resolver.go` patch)
The existing `sendBlockedResponse` only synthesizes an `A` record returning `0.0.0.0`.
It must also synthesize an `AAAA` record returning `::` (all-zeros IPv6) when the query
type is `AAAA` or when responding to ANY queries on a blocked domain.

Behavior per query type on a blocked domain:
| Query Type | Response |
|-----------|----------|
| A | NOERROR, A 0.0.0.0, TTL 3600 |
| AAAA | NOERROR, AAAA ::, TTL 3600 |
| HTTPS (65) | NOERROR, empty answer section |
| SVCB | NOERROR, empty answer section |
| ANY | NOERROR, A 0.0.0.0 + AAAA ::, TTL 3600 |
| Other | Forward to upstream normally |

### 2. HTTPS / SVCB Record Nullification (`src/dnsd/resolver.go` patch)
DNS query type 65 (`HTTPS`) and type 64 (`SVCB`) records are used by browsers to discover
DoH endpoints and Encrypted Client Hello (ECH) parameters. For any blocked domain, intercept
these queries and return `NOERROR` with an empty answer section rather than forwarding.

This prevents browsers from bootstrapping DoH or ECH for domains that are already blocked
at the A/AAAA level.

### 3. DoH Canary Domain Interception (`src/main.go` or `src/dnsd/resolver.go`)
The following domains are added to a hardcoded built-in blocklist that cannot be overridden
by the user whitelist:

```
use-application-dns.net      # Firefox DoH opt-out signal
dns.google                   # Chrome DoH bootstrap
cloudflare-dns.com           # Browser DoH bootstrap
doh.opendns.com              # OpenDNS DoH bootstrap
```

When Firefox queries `use-application-dns.net` and receives `NXDOMAIN` or `0.0.0.0`, it
automatically disables DoH and falls back to the OS resolver (i.e. Blackhole). Chrome uses
a similar mechanism for `dns.google`.

These are treated as `A → 0.0.0.0` blocks. They are **not** user-removable.

### 4. In-Memory DNS Response Cache (`src/dnsd/dns_cache.go`)
A bounded, concurrent LRU cache storing upstream DNS responses keyed by
`(qname, qtype, qclass)`. Maximum 4,096 entries (~8 MB worst case).

**Cache behavior:**
- On cache hit: return the cached response with TTL decremented to reflect elapsed time.
  If remaining TTL < 10 s, trigger an async background refresh goroutine and still return
  the stale-but-valid cached value.
- On cache miss: forward to upstream, cache the response, return to client.
- Blocked domains are **never** cached (they are synthesized inline).
- Cache is flushed atomically on blocklist reload.
- Thread-safe via `sync.RWMutex`.

### 5. Race-Forwarding Upstream with Failover (`src/dnsd/forwarder.go`)
Replace the current sequential `forwardQuery` loop with a race-forward strategy:

**Default upstreams** (configurable via `exclusions.json` or future config):
```
1.1.1.1:53   (Cloudflare)
9.9.9.9:53   (Quad9)
8.8.8.8:53   (Google)
```

**Algorithm:**
1. Send the query simultaneously to all configured upstreams.
2. Return the first response received.
3. If no response arrives within 500 ms, return `SERVFAIL` to the client.
4. Upstreams are updated dynamically via the existing `SCDynamicStore` VPN monitor (4.2).

### 6. Multi-Interface DNS Hooking (`MenuBar/DNSHelper.swift` patch)
The existing `DNSHelper.swift` applies `127.0.0.1` only to the current default route
interface. It must enumerate all active network services and apply the override to each.

**Algorithm:**
1. Execute `networksetup -listallnetworkservices` to get service names.
2. For each service, execute `networksetup -getinfo "<service>"` to check if it has an
   active IP address (i.e. is connected).
3. For connected services, apply: `networksetup -setdnsservers "<service>" 127.0.0.1`
4. On protection disable: `networksetup -setdnsservers "<service>" Empty` for each service.
5. Store the list of hooked services in `UserDefaults` so teardown can restore exactly the
   services that were modified.

### 7. Fail-Safe Shutdown Signal Handler (`src/main.go` patch)
Add `os/signal` handlers for `SIGTERM`, `SIGINT`, and `SIGHUP`. On receipt of any signal:

1. Log the signal received.
2. For each network service previously hooked (read from a shared state var set at startup):
   execute `networksetup -setdnsservers "<service>" Empty` via `os/exec`.
3. Stop the VPN monitor.
4. Close the UDP listener.
5. Exit with code 0.

This guarantees the user's DNS is always restored to automatic DHCP even if the daemon
is killed, updated, or the Mac is shut down.

---

## Data Flow

```
[UDP Query arrives]
       ↓
  Check blocked? ──yes──→ Synthesize A/AAAA/HTTPS response → reply
       ↓ no
  Check DNS cache ──hit──→ Decrement TTL → reply (+ async refresh if TTL < 10s)
       ↓ miss
  Race-forward to [1.1.1.1, 9.9.9.9, 8.8.8.8] ──first wins──→ cache → reply
```

---

## Testing Requirements
- Unit test: AAAA query on blocked domain returns `::` not forwarded upstream
- Unit test: HTTPS (type 65) query on blocked domain returns NOERROR + empty answer
- Unit test: `use-application-dns.net` resolves to `0.0.0.0` and is not user-overridable
- Unit test: DNS cache returns decremented TTL on cache hit
- Unit test: DNS cache triggers async refresh when TTL < 10 s
- Unit test: race-forwarder returns first response and cancels remaining goroutines
- Unit test: SIGTERM handler calls `networksetup -setdnsservers Empty` for each hooked service
- Integration test: multi-interface hook applies to all connected services simultaneously

---

## Files Created / Modified
| Action | Path |
|--------|------|
| Create | `src/dnsd/dns_cache.go` |
| Create | `src/dnsd/dns_cache_test.go` |
| Create | `src/dnsd/forwarder.go` |
| Create | `src/dnsd/forwarder_test.go` |
| Modify | `src/dnsd/resolver.go` (dual A+AAAA, HTTPS/SVCB nullification, canary domains) |
| Modify | `src/main.go` (signal handler, wire new forwarder and cache) |
| Modify | `MenuBar/DNSHelper.swift` (multi-interface enumeration) |
