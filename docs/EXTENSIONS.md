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

To append a custom filter, implement the `Filter` interface and inject it into the `FilterChain` instantiated in `daemon.go` during `runMessageLoop`.

## Blocklist Parser Interface

By default, Blackhole supports standard hosts files and Pi-hole gravity lists. To support proprietary or custom threat feed formats (e.g., YAML-based enterprise feeds), you can implement the `ListParser` interface:

```go
type ListParser interface {
    Parse(r io.Reader, onDomain func(string)) error
}
```

Your parser should read from `r` and call `onDomain(domain)` for every domain that needs to be blocked. You can then swap out `PiHoleParser` in `gravity.go` with your custom parser implementation.

## IPC API

Blackhole exposes a Unix domain socket for Inter-Process Communication (IPC), defined in `ipc_server.go`. This API allows external tools to interact with the running daemon without modifying its configuration files or restarting it.

### Capabilities:
- **Telemetry and Metrics:** Stream real-time DNS query logs, latency metrics, and block rates.
- **Dynamic Allowlisting/Blocklisting:** Add or remove domains from the active lists on the fly.
- **State Management:** Pause or resume filtering dynamically.

To use the IPC API, connect to the socket file specified in your daemon configuration (typically `/tmp/blackholed.sock`) and send JSON-RPC payloads as defined in the IPC handlers.
