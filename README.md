# Blackhole

[![Go Report Card](https://goreportcard.com/badge/github.com/blackhole/blackhole)](https://goreportcard.com/report/github.com/blackhole/blackhole)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Blackhole** is a lightweight, high-performance local DNS daemon and adblocker designed specifically for macOS. It sinks unwanted domains into the void, giving you back control over your network traffic.

## Architecture

Blackhole is fundamentally built as an ultra-low-latency DNS forwarder augmented with dynamic, concurrent filtering pipelines. 

### Core Components

1. **DNS Cache & FilterEngine (`dns_cache.go`, `gravity.go`)**
   The heart of Blackhole is an optimized concurrent trie and local DNS cache that avoids heap allocations on the hot path. The `FilterEngine` efficiently evaluates domains against millions of blocklist entries.
   - **Multi-Reader Gravity:** Blocklists are downloaded asynchronously, cached per-source, and merged entirely using `io.MultiReader` into memory to prevent single-source failure regressions.
2. **Dynamic Extension Chain (`forwarder.go`, `daemon.go`)**
   Traffic traverses a highly extensible `FilterChain`. Extensions and custom filters can register themselves using `GetFilters()`, enabling enterprise proxying and advanced metrics gathering without fork-bombing the core logic.
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
