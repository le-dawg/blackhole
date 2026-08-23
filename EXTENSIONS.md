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
    "github.com/blackhole/blackhole/src/dnsd"
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

## Blocklist Parser Interface

By default, Blackhole supports standard hosts files and Pi-hole gravity lists. To support proprietary or custom threat feed formats (e.g., YAML-based enterprise feeds), you can implement the `ListParser` interface:

```go
type ListParser interface {
    Parse(r io.Reader, onDomain func(string)) error
}
```

Your parser should read from `r` and call `onDomain(domain)` for every domain that needs to be blocked. Instead of modifying `gravity.go` directly, you can inject a custom parser at runtime using the `RegisterParserForURL` API. Create your own parser struct that implements the `ListParser` interface and pass it to your gravity or blocklist instance:

```go
// Define your custom parser
type MyParser struct {}

func (p MyParser) Parse(r io.Reader, onDomain func(string)) error {
    // Custom parsing logic here
    // call onDomain(domain) for each domain found
    return nil
}

// Inject it using the API
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
    "com.apple.Safari": 120,
    "com.google.Chrome": 80
  },
  "windowStart": "2026-08-22T22:11:51Z"
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
| `status` | String | Outcome (`Allowed`, `Blocked`, `Excluded`) |
| `processName` | String | Name of the process making the query |
| `bundleId` | String | Bundle identifier of the app making the query |
| `latencyMs` | Float | Resolution latency in milliseconds |

The daemon emits the following status values:
- `Allowed`: Normal DNS resolution via configured upstreams.
- `Blocked`: Domain blocked and sinkholed to `0.0.0.0`.
- `Excluded`: Query allowed via app-specific bypass rule (process name or bundle ID).

**Example Response Payload (Stream):**
```json
{"timestamp":"2026-08-23T22:11:51Z","domain":"ads.example.com","queryType":1,"status":"Blocked","processName":"Safari","bundleId":"com.apple.Safari","latencyMs":1.5}
{"timestamp":"2026-08-23T22:11:52Z","domain":"example.com","queryType":28,"status":"Allowed","processName":"Chrome","bundleId":"com.google.Chrome","latencyMs":12.1}
{"timestamp":"2026-08-23T22:11:53Z","domain":"tracking.slack.com","queryType":1,"status":"Excluded","processName":"Slack","bundleId":"com.tinyspeck.slackmacgap","latencyMs":2.4}
```

### `POST /pause`

Pauses DNS filtering dynamically for a specified duration.
**Content-Type:** `application/json`

**Error Codes:**
- `405 Method Not Allowed`: If a method other than POST is used.
- `400 Bad Request`: If the JSON payload is malformed or missing required fields.

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
