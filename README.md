# Blackhole

[![Go Report Card](https://goreportcard.com/badge/github.com/blackhole/blackhole)](https://goreportcard.com/report/github.com/blackhole/blackhole)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Blackhole** is a lightweight, high-performance local DNS daemon and adblocker designed specifically for macOS. It sinks unwanted domains into the void, giving you back control over your network traffic.

## Architecture

Blackhole is fundamentally built as an ultra-low-latency DNS forwarder augmented with extensible filtering pipelines. 

### Core Components

1. **DNS Cache & FilterEngine (`dns_cache.go`, `gravity.go`)**
   The heart of Blackhole is an optimized concurrent trie and local DNS cache that avoids heap allocations on the hot path. The `FilterEngine` efficiently evaluates domains against millions of blocklist entries.
   - **Multi-Reader Gravity:** Blocklists are downloaded serially, cached per-source, merged via io.MultiReader, and swapped into an in-memory trie atomically to prevent single-source failure regressions.
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

Temporarily pauses DNS blocking protection for a given duration (all queries pass through to upstream resolvers while paused).

**Error Codes:**
- `405 Method Not Allowed`: If called with any HTTP method other than `POST`.
- `400 Bad Request`: If the request body contains malformed JSON, unparseable data, or trailing/composite JSON documents (e.g. `{}{"durationSeconds":5}`).

**Request Behavior:**
- Passing `"durationSeconds": <int > 0>` pauses blocking protection for that duration.
- Passing `"durationSeconds": 0`, a negative value, or `{}` unpauses and resumes protection immediately.
- Successful requests return status `200 OK` with JSON body `{"ok": true}` and `Content-Type: application/json`.

**Request Payload (JSON):**
```json
{
  "durationSeconds": 300
}
```

**Response Payload (JSON):**
`HTTP 200 OK`
**Content-Type:** `application/json`

```json
{
  "ok": true
}
```

### Get Statistics
`GET /stats`

Retrieves current operational metrics and statistics.
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
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": 890,
    "/Applications/Spotify.app/Contents/MacOS/Spotify": 120
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
| `timestamp` | String | RFC3339Nano timestamp of the query (e.g., `2026-08-25T22:11:51.123456789Z`) |
| `domain` | String | The requested domain name |
| `queryType` | Integer | DNS query type (e.g., 1 for A, 28 for AAAA) |
| `status` | String | Outcome (`Allowed`, `Blocked`, `Excluded`) |
| `processName` | String | Client process executable binary path (resolved via proc_pidpath, and appended with "-[pattern]" when matched against a configured CLI pattern tag, e.g. "/Applications/Safari.app/Contents/MacOS/Safari" or "/usr/bin/python3-litellm"), or "Unknown" if resolution was dropped or unavailable |
| `bundleId` | String | Bundle identifier of the app making the query |
| `latencyMs` | Float | Resolution latency in milliseconds |

The daemon emits the following status values:
- `Allowed`: Normal DNS resolution via configured upstreams.
- `Blocked`: Domain blocked and sinkholed to `0.0.0.0`.
- `Excluded`: Query allowed via app-specific bypass rule (process name or bundle ID).

**Response Payload (`application/x-ndjson`):**
```ndjson
{"timestamp":"2026-08-23T22:11:51Z","domain":"ads.example.com","queryType":1,"status":"Blocked","processName":"/Applications/Safari.app/Contents/MacOS/Safari","bundleId":"com.apple.Safari","latencyMs":1.5}
{"timestamp":"2026-08-23T22:11:52Z","domain":"example.com","queryType":28,"status":"Allowed","processName":"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome","bundleId":"com.google.Chrome","latencyMs":12.1}
{"timestamp":"2026-08-23T22:11:53Z","domain":"slack-msgs.com","queryType":1,"status":"Excluded","processName":"/Applications/Slack.app/Contents/MacOS/Slack","bundleId":"com.tinyspeck.slackmacgap","latencyMs":4.2}
```
