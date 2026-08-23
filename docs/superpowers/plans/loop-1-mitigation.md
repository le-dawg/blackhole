# Loop 1: Structural Mitigation Plan

**Objective:** Execute the deepest structural overhaul to achieve CNCF-level OSS maturity. Eradicate all runtime risks identified by Codex (Kaminsky poisoning, global locking, privilege escalation, detached tasks, silent transports) and implement world-class blocklist lifecycle management.

## Phase 1: Core Security & Concurrency (Agent: The Go Sentinel)
**Goal:** Eradicate Kaminsky vulnerabilities, eliminate 50ms locks, and authenticate the IPC socket.
- [ ] **Task 1.1 (Kaminsky Fix):** Implement `validateDNSResponse` in `forwarder.go`. Ensure strict matching of TXID, QNAME, QTYPE, and QCLASS against the upstream response, including bailiwick constraints.
- [ ] **Task 1.2 (Lock-Free Tracking):** Refactor `process_monitor.go`. Move the heavy `proc_listpids` scanning to an asynchronous background ticker. Store the mapping in an `atomic.Pointer` so that `GetProcessInfoForPort` becomes O(1) and completely lock-free on the DNS hot-path.
- [ ] **Task 1.3 (IPC Auth):** Create `AuthenticatedUnixListener` in `ipc_server.go`. Intercept `Accept()` and execute `syscall.GetsockoptXucred(fd, SOL_LOCAL, LOCAL_PEERCRED)` to cryptographically verify the caller's UID before accepting the connection.

## Phase 2: macOS Systems Architecture (Agent: The macOS Engineer)
**Goal:** Achieve root-less privilege separation, structured concurrency, and memory-safe transport.
- [ ] **Task 2.1 (Privilege Drop):** Rework `DNSHelper.swift`. Delete the `networksetup` shell-out. Implement `NEDNSSettingsManager` to securely install the local DNS configuration profile without running as root.
- [ ] **Task 2.2 (Structured Concurrency):** Refactor `AppViewModel`, `IPCClient`, and `ExclusionModel`. Replace every instance of `Task.detached` with `@MainActor`-bound `Task {}` blocks. Ensure proper lifecycle cancellation via `Task.cancel()`.
- [ ] **Task 2.3 (NWConnection):** Rewrite `UnixSocketTransport.swift`. Replace raw C-sockets with Apple's `Network` framework (`NWConnection` + `NWEndpoint.unix(path:)`), implementing safe async/await continuations and byte-buffering.

## Phase 3: Blocklist & Pi-Hole Integration (Agent: The Gravity Engineer)
**Goal:** Achieve zero-downtime blocklist updates and seamless Pi-hole extension compatibility.
- [ ] **Task 3.1 (Filter Interfaces):** Implement the `Filter` and `FilterChain` interfaces in `forwarder.go` to abstract all blocking logic.
- [ ] **Task 3.2 (Atomic Swaps):** Refactor `gravity.go` to use a `FilterEngine` backed by `sync/atomic.Value`. Ensure background cron jobs build the Radix tree completely in memory before performing a zero-downtime pointer swap.
- [ ] **Task 3.3 (Pi-Hole Parsers):** Implement `BlocklistParser` and `PiHoleParser` to cleanly ingest raw `/etc/hosts` formats and Pi-hole gravity lists, stripping comments and normalizing domains.

