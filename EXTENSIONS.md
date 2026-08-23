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
- If a filter determines the request should be blocked, it sets `block = true` and returns a synthesized `resp` byte slice.
- If `err != nil`, the chain aborts.
- If all filters pass, the request is forwarded to upstream servers via `RaceForward`.

To append a custom filter, implement the `Filter` interface and register it via `RegisterFilter(f Filter)`. `daemon.go` appends these to the active chain using `GetFilters()`.

## Blocklist Parser Interface

By default, Blackhole supports standard hosts files and Pi-hole gravity lists. To support proprietary or custom threat feed formats, implement the `ListParser` interface:

```go
type ListParser interface {
    Parse(r io.Reader, onDomain func(string)) error
}
```

## IPC API

Blackhole exposes a Unix domain socket for Inter-Process Communication (IPC). This API allows external tools to interact with the running daemon dynamically.

**Important Note:** The daemon uses REST/HTTP over a Unix socket, typically located at `/var/run/blackholed.sock` (NOT `/tmp/` and NOT JSON-RPC). 

### Capabilities:
- **Telemetry and Metrics:** Stream real-time DNS query logs, latency metrics, and block rates.
- **Dynamic Allowlisting/Blocklisting:** Add or remove domains from the active lists.
- **State Management:** Pause or resume filtering dynamically.
