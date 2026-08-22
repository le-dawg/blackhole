# QoS Downgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Downgrade the QoS for periodic stat/query fetching to improve energy efficiency.

**Architecture:** Change the dispatch queue QoS from `.userInitiated` to `.utility` in `MenuBar/Services/IPCClient.swift` for the periodic fetches (`fetchStats` and `fetchQueries`).

**Tech Stack:** Swift, macOS

## Global Constraints

- No behavioral changes, only QoS adjustments for energy efficiency.

---

### Task 1: Downgrade QoS in IPCClient

**Files:**
- Modify: `MenuBar/Services/IPCClient.swift:39-84`

**Interfaces:**
- Consumes: N/A
- Produces: N/A

- [ ] **Step 1: Modify fetchStats implementation**

Update `fetchStats()` in `MenuBar/Services/IPCClient.swift` to use `.utility` QoS instead of `.userInitiated`.

```swift
    private func fetchStats() {
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/stats", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601 // Assume ISO8601 or similar if needed. Actually the spec doesn't say, default is fine.
            if let stats = try? decoder.decode(StatsResponse.self, from: data) {
                DispatchQueue.main.async { self?.currentStats = stats }
            }
        }
    }
```

- [ ] **Step 2: Modify fetchQueries implementation**

Update `fetchQueries()` in `MenuBar/Services/IPCClient.swift` to use `.utility` QoS instead of `.userInitiated`.

```swift
    private func fetchQueries() {
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/queries", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            guard let str = String(data: data, encoding: .utf8) else { return }
            
            let lines = str.split(separator: "\n")
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            var parsed: [QueryRecord] = []
            
            for line in lines {
                if let d = line.data(using: .utf8), let rec = try? decoder.decode(QueryRecord.self, from: d) {
                    parsed.append(rec)
                }
            }
            
            DispatchQueue.main.async { self?.queries = parsed.reversed() } // newest first
        }
    }
```

- [ ] **Step 3: Commit**

```bash
git add MenuBar/Services/IPCClient.swift
git commit -m "chore: downgrade QoS to .utility for periodic fetches to improve energy efficiency"
```
