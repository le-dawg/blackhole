# Blackhole Extensions Guide

This document explains how to extend Blackhole's functionality through custom filters, custom list parsers, and the IPC API.

## FilterChain Architecture

Blackhole's packet forwarding path is governed by a flexible `FilterChain`. The core of this system is the `Filter` interface:

```go
type Filter interface {
    Process(req []byte) (resp []byte, block bool, err error)
}
```

When a DNS request arrives, it is passed through the `FilterChain`.
- If a filter determines the request should be blocked, it sets `block = true` and returns a synthesized `resp` byte slice (e.g., returning 0.0.0.0).
- If `err != nil`, the chain aborts.
- If all filters pass, the request is forwarded to upstream servers via `RaceForward`.

### Using `RegisterFilter` in a Custom `main.go`

Instead of manually editing `daemon.go`, you can create a custom `main.go` that imports Blackhole and registers your custom filter before starting the daemon. The daemon will automatically append registered filters to the active chain using `GetFilters()`.

```go
package main

import (
    "log"
    "blackhole/src/dnsd"
)

// 1. Define your custom filter
type MyCustomFilter struct {}

func (f MyCustomFilter) Process(req []byte) (resp []byte, block bool, err error) {
    // Custom filter logic here
    // e.g., block specific requests and return synthesized DNS response
    return nil, false, nil
}

func main() {
    // 2. Register your custom filter globally
    dnsd.RegisterFilter(MyCustomFilter{})

    // 3. Start the daemon as usual
    cfg := dnsd.DefaultConfig()
    daemon := dnsd.NewDaemon(cfg)
    
    // setup context and start the daemon...
}
```

## Blocklist Parser Interfaces

By default, Blackhole supports standard `/etc/hosts` files, Pi-hole gravity lists, and AdGuard syntax lists. To support proprietary or custom threat feed formats (e.g., YAML-based enterprise feeds), you can implement `ListParser` or `RuleAwareListParser`:

### `ListParser`
```go
type ListParser interface {
    Parse(r io.Reader, onDomain func(string)) error
}
```

### `RuleAwareListParser`
```go
type RuleAwareListParser interface {
    ParseRules(r io.Reader, onBlock func(string), onException func(string)) error
}
```

Your parser should read from `r` and call `onBlock(domain)` for every domain to block and `onException(domain)` for every domain exception (allowlist). Instead of modifying `gravity.go` directly, you can inject a custom parser at runtime using the `RegisterParserForURL` API:

```go
// Define your custom parser
type MyParser struct {}

func (p MyParser) ParseRules(r io.Reader, onBlock func(string), onException func(string)) error {
    // Custom parsing logic here
    // call onBlock(domain) or onException(domain)
    return nil
}

// Inject it using the API (longest prefix matching)
dnsd.RegisterParserForURL("https://example.com/list", MyParser{})
```

## IPC API

Blackhole exposes a Unix domain socket for Inter-Process Communication (IPC), defined in `ipc_server.go`. This API allows external tools to interact with the running daemon without modifying its configuration files or restarting it.

**Important Note:** The daemon uses REST/HTTP over a Unix socket, typically located at `/var/run/blackhole.sock` (NOT `/tmp/` and NOT JSON-RPC).

### `GET /stats`

Returns aggregated telemetry, metrics, and block rates over the past 24 hours.
**Content-Type:** `application/json`

**Error Codes:**
- `405 Method Not Allowed`: If a method other than GET is used.

**StatsSnapshot Schema:**

| Field | Type | Description |
|---|---|---|
| `total` | Integer | Total number of queries processed over the rolling 24-hour window |
| `blocked` | Integer | Total number of queries blocked/sinkholed over the rolling 24-hour window |
| `blockPercent` | Float | Percentage of queries blocked (`(blocked / total) * 100.0`) |
| `topDomains` | Object | Map of top 5 most frequently blocked domains to block counts over the rolling 24-hour window |
| `topApps` | Object | Map of top 5 client executable binary paths (with any matched CLI pattern tags appended, e.g. "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" or "/usr/bin/python3-litellm") generating the most blocked queries to blocked counts over the rolling 24-hour window |
| `windowStart` | String | RFC3339 timestamp marking the start of the hourly-bucketed 24-hour aggregation window (`now.Add(-24h).Truncate(1h)`) |

**Example Response:**
```json
{
  "total": 1000,
  "blocked": 250,
  "blockPercent": 25.0,
  "topDomains": {
    "ads.example.com": 100,
    "tracker.example.com": 50
  },
  "topApps": {
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": 120,
    "/Applications/Spotify.app/Contents/MacOS/Spotify": 80
  },
  "windowStart": "2026-08-22T22:00:00Z"
}
```

### `GET /queries`

Returns a stream of recent DNS query logs.
**Content-Type:** `application/x-ndjson`

**Snapshot Semantics:**
This endpoint outputs a point-in-time snapshot of the circular ring buffer as NDJSON and closes the connection upon completion.

**Error Codes:**
- `405 Method Not Allowed`: If a method other than GET is used.

This endpoint returns a newline-delimited JSON stream where each line is a `QueryRecord` object.

**QueryRecord Schema:**

| Field | Type | Description |
|---|---|---|
| `timestamp` | String | ISO-8601 timestamp of the query |
| `domain` | String | The requested domain name |
| `queryType` | Integer | DNS query type (e.g., 1 for A, 28 for AAAA) |
| `status` | String | Outcome (`Allowed`, `Blocked`, `Excluded`, `Servfail`) |
| `processName` | String | Client process executable binary path (resolved via proc_pidpath, and appended with "-[pattern]" when matched against a configured CLI pattern tag, e.g. "/Applications/Safari.app/Contents/MacOS/Safari" or "/usr/bin/python3-litellm"), or "Unknown" if resolution was dropped or unavailable |
| `bundleId` | String | Bundle identifier of the app making the query |
| `latencyMs` | Float | Resolution latency in milliseconds |

The daemon emits the following status values:
- `Allowed`: Normal DNS resolution via configured upstreams.
- `Blocked`: Domain blocked and sinkholed to `0.0.0.0` or `::`.
- `Excluded`: Query allowed via app-specific bypass rule (process name or bundle ID).
- `Servfail`: Upstream resolution failure, filter pipeline error, format violation, or queue overload.

**Example Response Payload (Stream):**
```ndjson
{"timestamp":"2026-08-23T22:11:51Z","domain":"ads.example.com","queryType":1,"status":"Blocked","processName":"/Applications/Safari.app/Contents/MacOS/Safari","bundleId":"com.apple.Safari","latencyMs":1.5}
{"timestamp":"2026-08-23T22:11:52Z","domain":"example.com","queryType":28,"status":"Allowed","processName":"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome","bundleId":"com.google.Chrome","latencyMs":12.1}
{"timestamp":"2026-08-23T22:11:53Z","domain":"slack-msgs.com","queryType":1,"status":"Excluded","processName":"/Applications/Slack.app/Contents/MacOS/Slack","bundleId":"com.tinyspeck.slackmacgap","latencyMs":4.2}
{"timestamp":"2026-08-23T22:11:54Z","domain":"unreachable.internal","queryType":1,"status":"Servfail","processName":"/usr/bin/curl","bundleId":"","latencyMs":501.2}
```

### `POST /pause`

Pauses DNS filtering dynamically for a specified duration.
**Content-Type:** `application/json`

**Error Codes:**
- `405 Method Not Allowed`: If called with any HTTP method other than `POST`.
- `400 Bad Request`: If the request body contains malformed JSON, unparseable data, or trailing/composite JSON documents (e.g. `{}{"durationSeconds":5}`).

**Request Behavior:**
- Passing `"durationSeconds": <int > 0>` pauses blocking protection for that duration.
- Passing `"durationSeconds": 0`, a negative value, or `{}` unpauses and resumes protection immediately.
- Successful requests return status `200 OK` with JSON body `{"ok": true}` and `Content-Type: application/json`.

**Example Request Payload:**
```json
{
  "durationSeconds": 300
}
```

**Example Response:**
`HTTP 200 OK`
**Content-Type:** `application/json`

```json
{
  "ok": true
}
```

To use the IPC API, connect to the socket file specified in your daemon configuration (typically `/var/run/blackhole.sock`) and send HTTP payloads to these endpoints.
