# Blackhole

[![Go Report Card](https://goreportcard.com/badge/github.com/blackhole/blackhole)](https://goreportcard.com/report/github.com/blackhole/blackhole)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Blackhole** is a lightweight, high-performance local DNS daemon and adblocker designed specifically for macOS. It sinks unwanted domains into the void, giving you back control over your network traffic.

## Architecture

Blackhole is fundamentally built as an ultra-low-latency DNS forwarder augmented with extensible filtering pipelines. 

### Core Components

1. **DNS Cache & FilterEngine (`dns_cache.go`, `gravity.go`)**
   The heart of Blackhole is an optimized concurrent trie and local DNS cache that avoids heap allocations on the hot path. The `FilterEngine` efficiently evaluates domains against millions of blocklist entries.
   - **Multi-Reader Gravity:** Blocklists are downloaded serially, cached per-source, and merged via `io.MultiReader` and compiled into an atomic state file to prevent single-source failure regressions.
2. **Dynamic Extension Chain (`forwarder.go`, `daemon.go`)**
   Traffic traverses a highly extensible `FilterChain`. Extensions and custom filters can register themselves using `RegisterFilter()`, enabling enterprise proxying and advanced metrics gathering without fork-bombing the core logic.
3. **IPC Interop (`ipc_server.go`)**
   Provides a stateless HTTP-over-Unix-socket interface that avoids legacy JSON-RPC and transient `/tmp` socket bugs, binding securely to `/var/run/blackhole.sock`.
4. **App-Aware Exclusions (`process_monitor.go`, `exclusions.go`)**
   Taps into macOS-native APIs (like `lsof` and process inspection) to allow bypass rules per-app (e.g., allowlisting Slack while blocking ads everywhere else).

## Getting Started

### Prerequisites

- Go 1.26 or higher
- macOS environment

### Installation

You can install Blackhole using the provided installation script:

```bash
./install.sh
```

Alternatively, you can build from source:

```bash
make build
make install
```

## Usage

Once installed, the `blackholed` daemon runs in the background. You can interact with the system using the `blackhole` CLI tool.

## IPC Endpoints

The service exposes the following Inter-Process Communication (IPC) endpoints for control and monitoring:

### Pause Service
`POST /pause`

Temporarily pauses the service or specific subsystems for a given duration.

**Error Codes:**
- `405 Method Not Allowed`: If a method other than POST is used.
- `400 Bad Request`: If the JSON payload is malformed or missing required fields.

**Request Payload (JSON):**
```json
{
  "durationSeconds": 300
}
```

**Response Payload (JSON):**
`HTTP 200 OK`

```json
{
  "ok": true
}
```

### Get Statistics
`GET /stats`

Retrieves current operational metrics and statistics.

**Error Codes:**
- `405 Method Not Allowed`: If a method other than GET is used.

**Response Payload (JSON):**
```json
{
  "total": 54210,
  "blocked": 10452,
  "blockPercent": 19.28,
  "topDomains": {
    "ads.example.com": 1234,
    "tracker.example.com": 567
  },
  "topApps": {
    "Google Chrome": 890,
    "Spotify": 120
  },
  "windowStart": "2023-08-23T15:00:00Z"
}
```

### Get Queries
`GET /queries`

Retrieves a snapshot of recent DNS queries in newline-delimited JSON (NDJSON) format.

**Snapshot Semantics:**
This endpoint outputs a point-in-time snapshot of the circular ring buffer as NDJSON and closes the connection upon completion.

**Error Codes:**
- `405 Method Not Allowed`: If a method other than GET is used.

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

**Response Payload (`application/x-ndjson`):**
```ndjson
{"timestamp":"2026-08-23T22:05:12Z","domain":"ads.example.com","queryType":1,"status":"Blocked","processName":"Google Chrome","bundleId":"com.google.Chrome","latencyMs":0.5}
{"timestamp":"2026-08-23T22:05:13Z","domain":"api.github.com","queryType":1,"status":"Allowed","processName":"Terminal","bundleId":"com.apple.Terminal","latencyMs":12.3}
{"timestamp":"2026-08-23T22:05:14Z","domain":"tracking.slack.com","queryType":1,"status":"Excluded","processName":"Slack","bundleId":"com.tinyspeck.slackmacgap","latencyMs":2.4}
```
