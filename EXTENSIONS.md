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

### Capabilities:
- **Telemetry and Metrics (`/stats`, `/queries`):** Stream real-time DNS query logs, latency metrics, and block rates.
- **State Management (`/pause`):** Pause or resume filtering dynamically.

To use the IPC API, connect to the socket file specified in your daemon configuration (typically `/var/run/blackhole.sock`) and send HTTP payloads as defined in the IPC handlers.
