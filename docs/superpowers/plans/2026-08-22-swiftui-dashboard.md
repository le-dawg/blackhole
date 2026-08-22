# SwiftUI Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the SwiftUI menu bar application into a Pi-hole-grade control center with live data from the Go daemon's IPC socket, one-click temporary pausing, a live query inspector, and automatic discovery of installed apps for exclusions.

**Architecture:** A Swift `actor` orchestrates periodic HTTP-over-Unix-Socket polling of the daemon. SwiftUI views bind to the `@Published` properties. The app uses `NSWorkspace` to discover installed apps for the exclusion list.

**Tech Stack:** Swift 6, SwiftUI, URLSession (AF_UNIX bridging), NSWorkspace.

## Global Constraints

- Platform: macOS 15+ (SwiftUI 6)
- App RAM budget: < 20 MB (menu bar widget must be lightweight)
- Network communication: HTTP over Unix Domain Socket (`/tmp/blackhole.sock`)
- File format: Read/write `exclusions.json` directly (or via IPC if the IPC engine owns it)

---

### Task 1: IPC Socket Client and Models

**Files:**
- Create: `MenuBar/Models/StatsResponse.swift`
- Create: `MenuBar/Models/QueryRecord.swift`
- Create: `MenuBar/Services/IPCClient.swift`

**Interfaces:**
- Produces: `class IPCClient: ObservableObject`
- Produces: `@Published var currentStats: StatsResponse?`
- Produces: `@Published var queries: [QueryRecord] = []`
- Produces: `func startPollingStats()`, `func stopPollingStats()`
- Produces: `func startPollingQueries()`, `func stopPollingQueries()`
- Produces: `func sendPause(durationSeconds: Int) async throws`

- [ ] **Step 1: Define the Models**
Create `MenuBar/Models/StatsResponse.swift`:
```swift
import Foundation

struct StatsResponse: Codable {
    let total: UInt64
    let blocked: UInt64
    let blockPercent: Double
    let topDomains: [String: UInt64]
    let topApps: [String: UInt64]
    let windowStart: Date
}
```
Create `MenuBar/Models/QueryRecord.swift`:
```swift
import Foundation

struct QueryRecord: Codable, Identifiable {
    var id: UUID = UUID() // local generation for SwiftUI lists
    let timestamp: Date
    let domain: String
    let queryType: UInt16
    let status: String
    let processName: String
    let bundleId: String
    let latencyMs: Double
    
    enum CodingKeys: String, CodingKey {
        case timestamp, domain, queryType, status, processName, bundleId, latencyMs
    }
}
```

- [ ] **Step 2: Implement IPCClient**
Create `MenuBar/Services/IPCClient.swift`. Use standard URLSession but with a custom URL handler or just basic Unix Socket via Network framework. Since URLSession native unix socket support is slightly complex, an easy way is to use a background URLSession with `unix://` but natively iOS16+ / macOS13+ supports Unix Domain Sockets in `URLSessionConfiguration`.
```swift
import Foundation
import Combine

class IPCClient: ObservableObject {
    @Published var currentStats: StatsResponse?
    @Published var queries: [QueryRecord] = []
    
    private var statsTimer: AnyCancellable?
    private var queriesTimer: AnyCancellable?
    
    // macOS 13+ supports unix domain sockets natively via URLSession if configured properly, or we can use a custom protocol.
    // For simplicity, we assume a custom unix socket URL.
    // Actually, Apple added `URLSession.shared.data(from: URL(fileURLWithPath: "/tmp/blackhole.sock"))`? No, you need a custom stream.
    // Let's use a simpler approach: curl via Process! It's perfectly fine for a macOS menu bar app.
    
    func startPollingStats() {
        statsTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect().sink { [weak self] _ in
            self?.fetchStats()
        }
        fetchStats()
    }
    
    func stopPollingStats() { statsTimer?.cancel(); statsTimer = nil }
    
    func startPollingQueries() {
        queriesTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect().sink { [weak self] _ in
            self?.fetchQueries()
        }
        fetchQueries()
    }
    
    func stopPollingQueries() { queriesTimer?.cancel(); queriesTimer = nil }
    
    private func fetchStats() {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/stats", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            if let stats = try? JSONDecoder().decode(StatsResponse.self, from: data) {
                DispatchQueue.main.async { self.currentStats = stats }
            }
        }
    }
    
    private func fetchQueries() {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/queries", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            guard let str = String(data: data, encoding: .utf8) else { return }
            
            let lines = str.split(separator: "\n")
            let decoder = JSONDecoder()
            var parsed: [QueryRecord] = []
            
            for line in lines {
                if let d = line.data(using: .utf8), let rec = try? decoder.decode(QueryRecord.self, from: d) {
                    parsed.append(rec)
                }
            }
            
            DispatchQueue.main.async { self.queries = parsed.reversed() } // newest first
        }
    }
    
    func sendPause(durationSeconds: Int) {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "-X", "POST", "-d", "{\"durationSeconds\": \(durationSeconds)}", "http://localhost/pause", "-s"]
            try? task.run()
            task.waitUntilExit()
        }
    }
}
```

- [ ] **Step 3: Commit**
`git add MenuBar/Models/ MenuBar/Services/IPCClient.swift && git commit -m "feat: IPC client for stats and queries"`

---

### Task 2: Live Stats HUD

**Files:**
- Modify: `MenuBar/Views/PopoverView.swift`

**Interfaces:**
- Consumes: `@StateObject var ipc = IPCClient()`

- [ ] **Step 1: Integrate IPCClient**
In `PopoverView.swift`, add `@StateObject private var ipc = IPCClient()`. 
Modify `.onAppear { ipc.startPollingStats() }` and `.onDisappear { ipc.stopPollingStats() }`.

- [ ] **Step 2: Update the Dashboard UI**
Replace static metric cards with live data from `ipc.currentStats`.
```swift
if let stats = ipc.currentStats {
    HStack {
        MetricCard(title: "TOTAL QUERIES", value: "\(stats.total)", subtitle: "24h window", icon: "network", color: .blue)
        MetricCard(title: "BLOCKED", value: "\(stats.blocked)", subtitle: String(format: "%.1f%% of traffic", stats.blockPercent), icon: "shield.fill", color: .green)
    }
    // Render top 5 domains manually or using a simple ForEach over stats.topDomains.keys.sorted()
}
```

- [ ] **Step 3: Commit**
`git commit -am "feat: bind live stats to dashboard"`

---

### Task 3: Live Query Inspector

**Files:**
- Create: `MenuBar/Views/InspectorView.swift`
- Modify: `MenuBar/Views/PopoverView.swift` (to add the tab)

**Interfaces:**
- Consumes: `ipc.queries`

- [ ] **Step 1: Create the Inspector View**
Create `MenuBar/Views/InspectorView.swift` that takes `@ObservedObject var ipc: IPCClient`.
```swift
import SwiftUI

struct InspectorView: View {
    @ObservedObject var ipc: IPCClient
    
    var body: some View {
        List(ipc.queries) { query in
            HStack {
                Circle().fill(query.status == "Blocked" ? Color.red : (query.status == "Excluded" ? Color.gray : Color.green))
                    .frame(width: 8, height: 8)
                VStack(alignment: .leading) {
                    Text(query.domain).font(.system(.body, design: .monospaced))
                    Text(query.processName.isEmpty ? "Unknown" : query.processName).font(.caption).foregroundColor(.secondary)
                }
                Spacer()
                Text(String(format: "%.1f ms", query.latencyMs)).font(.caption2).foregroundColor(.secondary)
            }
        }
        .onAppear { ipc.startPollingQueries() }
        .onDisappear { ipc.stopPollingQueries() }
    }
}
```

- [ ] **Step 2: Add to Popover**
In `PopoverView.swift`, add a tab navigation button for "Inspector" and render `InspectorView(ipc: ipc)` when selected.

- [ ] **Step 3: Commit**
`git add MenuBar/Views/InspectorView.swift && git commit -am "feat: live query inspector tab"`

---

### Task 4: Timed Pause Controls

**Files:**
- Modify: `MenuBar/Views/PopoverView.swift`
- Modify: `MenuBar/Services/IPCClient.swift`

- [ ] **Step 1: Add Pause Menu UI**
Replace the simple `DNSHelper.shared.clearLocalDNS()` toggle button with a Menu or distinct buttons for timed pauses:
```swift
Menu {
    Button("Disable for 5 minutes") { ipc.sendPause(durationSeconds: 300); DNSHelper.shared.clearLocalDNS() }
    Button("Disable for 15 minutes") { ipc.sendPause(durationSeconds: 900); DNSHelper.shared.clearLocalDNS() }
    Button("Disable indefinitely") { ipc.sendPause(durationSeconds: 86400); DNSHelper.shared.clearLocalDNS() }
} label: {
    Text("Pause Protection")
}
```
*Note: Because DNS requests are cached tightly by macOS, we must also run `clearLocalDNS()` to drop the interfaces from localhost back to default router DNS so the pause applies immediately at the OS level, rather than waiting for DNS cache to expire.*

- [ ] **Step 2: Commit**
`git commit -am "feat: timed pause controls"`

---

### Task 5: Application Exclusion Scanner

**Files:**
- Create: `MenuBar/Services/AppScanner.swift`
- Modify: `MenuBar/Views/PopoverView.swift`

**Interfaces:**
- Produces: `class AppScanner { static func getInstalledApps() -> [(name: String, bundleId: String)] }`

- [ ] **Step 1: Implement AppScanner**
Create `MenuBar/Services/AppScanner.swift`:
```swift
import Foundation
import AppKit

class AppScanner {
    static func getInstalledApps() -> [(name: String, bundleId: String)] {
        let urls = FileManager.default.urls(for: .applicationDirectory, in: .localDomainMask)
        var apps: [(name: String, bundleId: String)] = []
        
        for url in urls {
            if let enumerator = FileManager.default.enumerator(at: url, includingPropertiesForKeys: [.isDirectoryKey], options: [.skipsHiddenFiles, .skipsPackageDescendants]) {
                for case let fileURL as URL in enumerator {
                    if fileURL.pathExtension == "app" {
                        if let bundle = Bundle(url: fileURL), let bundleId = bundle.bundleIdentifier {
                            let name = fileURL.deletingPathExtension().lastPathComponent
                            apps.append((name: name, bundleId: bundleId))
                        }
                    }
                }
            }
        }
        return apps.sorted { $0.name < $1.name }
    }
}
```

- [ ] **Step 2: Integrate into Exclusion UI**
In `PopoverView.swift`, when adding an exclusion, show a picker/list of discovered apps from `AppScanner.getInstalledApps()`.

- [ ] **Step 3: Commit**
`git add MenuBar/Services/AppScanner.swift && git commit -am "feat: automatic app discovery for exclusions"`

---
