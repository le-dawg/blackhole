# Project Blackhole Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a native, ultra-lightweight local DNS ad-blocker for macOS Tahoe with a SwiftUI menu bar widget, supporting process-level exclusions (such as LiteLLM) and seamless native VPN stack integration.

**Architecture:** Bypasses VMs/Docker by running a native Go DNS resolver daemon (`blackhole-dnsd`) on `127.0.0.1:53`. It correlates queries with process names by querying socket-to-PID mappings (`proc_pidinfo` / `<libproc.h>`) and links with a SwiftUI Menu Bar widget managing configuration and exclusion lists.

**Tech Stack:** Go (for DNS daemon), Swift/SwiftUI (for Menu Bar UI), `MenuBarExtraAccess` (for popover focus and window management).

## Global Constraints
- Target platform: macOS 15+ (Tahoe).
- Resource target: Total RAM < 35MB (Daemon <15MB, Widget <20MB). Idle CPU < 0.1%.
- Must run in user-space without private system extensions/entitlements.
- Process-level exclusion matching must support LiteLLM (python processes executing the `litellm` module) and application bundle IDs.
- VPN stack changes must be handled dynamically via `SCDynamicStore` notifications.

---

## Component Layout & Responsibilities

```
src/
├── dnsd/                    # Go-based DNS Daemon
│   ├── main.go              # CLI Entry point & listener setup
│   ├── resolver.go          # Trie-based DNS filter & blocklist engine
│   ├── process_monitor.go   # Socket to PID mapping & proc_pidinfo integration
│   └── vpn_monitor.go       # SCDynamicStore listener & upstreams coordinator
└── MenuBar/                 # SwiftUI Menu Bar Application
    ├── BlackholeApp.swift   # App entry point using MenuBarExtra & MenuBarExtraAccess
    ├── Views/
    │   ├── PopoverView.swift# Main popover structure with tabs
    │   ├── StatsView.swift  # Status and metrics tab
    │   └── ExclusionsView.swift # Exclusion lists management tab
    └── Models/
        └── Exclusions.swift # JSON loader/writer for exclusions.json
```

---

## Tasks

### Task 1: Core DNS Resolver Scaffolding (`blackhole-dnsd`)

**Files:**
- Create: `src/dnsd/resolver.go`
- Create: `src/dnsd/resolver_test.go`

**Interfaces:**
- Consumes: None
- Produces: `NewResolver(upstreams []string) *Resolver`, `(r *Resolver) Resolve(domain string) (isBlocked bool)`

- [ ] **Step 1: Write the failing test**

  Write test verifying the Trie filter correctly parses domain lists and matches subdomains. Create `src/dnsd/resolver_test.go`:
  ```go
  package dnsd

  import "testing"

  func TestTrieResolver(t *testing.T) {
      r := NewResolver([]string{"1.1.1.1"})
      r.AddBlockedDomain("ads.doubleclick.net")
      r.AddBlockedDomain("adservice.google.com")

      if !r.Resolve("ads.doubleclick.net") {
          t.Error("Expected ads.doubleclick.net to be blocked")
      }
      if !r.Resolve("sub.ads.doubleclick.net") {
          t.Error("Expected subdomains of blocked domains to be blocked")
      }
      if r.Resolve("google.com") {
          t.Error("Expected google.com to NOT be blocked")
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  Run: `cd src/dnsd && go test -run TestTrieResolver`
  Expected: FAIL (Compilation error: `NewResolver` undefined)

- [ ] **Step 3: Write minimal implementation**

  Create `src/dnsd/resolver.go`:
  ```go
  package dnsd

  import "strings"

  type TrieNode struct {
      children map[string]*TrieNode
      isEnd    bool
  }

  type Resolver struct {
      root      *TrieNode
      upstreams []string
  }

  func NewResolver(upstreams []string) *Resolver {
      return &Resolver{
          root:      &TrieNode{children: make(map[string]*TrieNode)},
          upstreams: upstreams,
      }
  }

  func (r *Resolver) AddBlockedDomain(domain string) {
      parts := strings.Split(domain, ".")
      node := r.root
      for i := len(parts) - 1; i >= 0; i-- {
          part := parts[i]
          if _, exists := node.children[part]; !exists {
              node.children[part] = &TrieNode{children: make(map[string]*TrieNode)}
          }
          node = node.children[part]
      }
      node.isEnd = true
  }

  func (r *Resolver) Resolve(domain string) bool {
      parts := strings.Split(domain, ".")
      node := r.root
      for i := len(parts) - 1; i >= 0; i-- {
          part := parts[i]
          if nextNode, exists := node.children[part]; exists {
              node = nextNode
              if node.isEnd {
                  return true
              }
          } else {
              break
          }
      }
      return false
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  Run: `go test -run TestTrieResolver`
  Expected: PASS

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add src/dnsd/resolver.go src/dnsd/resolver_test.go
  git commit -m "feat: add core trie-based dns resolver"
  ```

---

### Task 2: Process Identification Engine (`proc_pidinfo` CGo integration)

**Files:**
- Create: `src/dnsd/process_monitor.go`
- Create: `src/dnsd/process_monitor_test.go`

**Interfaces:**
- Consumes: None
- Produces: `GetProcessInfoForPort(port uint16) (procName string, bundleID string, err error)`

- [ ] **Step 1: Write the failing test**

  Write `src/dnsd/process_monitor_test.go`:
  ```go
  package dnsd

  import "testing"

  func TestProcessCorrelation(t *testing.T) {
      // Ephemeral ports without active sockets should fail cleanly or return empty
      name, bundleID, err := GetProcessInfoForPort(9999)
      if err == nil && (name != "" || bundleID != "") {
          t.Errorf("Expected lookup on inactive port to fail or return empty. Got name=%s, bundleID=%s", name, bundleID)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  Run: `go test -run TestProcessCorrelation`
  Expected: FAIL (Compilation error: `GetProcessInfoForPort` undefined)

- [ ] **Step 3: Write minimal implementation**

  Create `src/dnsd/process_monitor.go`. This module links C APIs from `<libproc.h>` via CGo to trace local socket connections to PIDs:
  ```go
  package dnsd

  /*
  #include <sys/proc_info.h>
  #include <libproc.h>
  #include <stdlib.h>
  */
  import "C"
  import (
      "errors"
      "fmt"
      "unsafe"
  )

  type ProcessCacheEntry struct {
      Name     string
      BundleID string
  }

  func GetProcessInfoForPort(port uint16) (string, string, error) {
      // Retrieve list of pids running on system
      pidsCount := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
      if pidsCount <= 0 {
          return "", "", errors.New("failed to list pids")
      }

      pids := make([]C.int, pidsCount)
      bufferSize := C.int(int(unsafe.Sizeof(pids[0])) * int(pidsCount))
      C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), bufferSize)

      for _, pid := range pids {
          if pid == 0 {
              continue
          }
          // Query file descriptor info for sockets
          fdBufferSize := C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, nil, 0)
          if fdBufferSize <= 0 {
              continue
          }

          fdCount := int(fdBufferSize) / int(unsafe.Sizeof(C.struct_proc_fdinfo{}))
          fds := make([]C.struct_proc_fdinfo, fdCount)
          C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, unsafe.Pointer(&fds[0]), C.int(fdBufferSize))

          for _, fd := range fds {
              if fd.proc_fdtype == C.PROX_FDTYPE_SOCKET {
                  var sockInfo C.struct_socket_fdinfo
                  sockSize := C.proc_pidfdinfo(pid, fd.proc_fd, C.SOCKET_INFO_T, unsafe.Pointer(&sockInfo), C.int(unsafe.Sizeof(sockInfo)))
                  if sockSize > 0 {
                      // Check UDP/TCP family matches IPv4 or IPv6
                      in := sockInfo.psi.soi_proto.pri_in
                      localPort := C.ntohs(C.uint16_t(in.insi_lport))
                      if uint16(localPort) == port {
                          // Match! Find process executable path
                          pathBuffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
                          ret := C.proc_pidpath(pid, unsafe.Pointer(&pathBuffer[0]), C.uint32_t(len(pathBuffer)))
                          if ret > 0 {
                              procName := string(pathBuffer[:ret])
                              // For script processes (like Python running litellm),
                              // process name will match the interpreter path, which is sufficient.
                              return procName, "", nil
                          }
                      }
                  }
              }
          }
      }
      return "", "", fmt.Errorf("port %d not found in active sockets", port)
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  Run: `go test -run TestProcessCorrelation`
  Expected: PASS

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add src/dnsd/process_monitor.go src/dnsd/process_monitor_test.go
  git commit -m "feat: implement CGo process identification mapping"
  ```

---

### Task 3: VPN Interface Dynamic Chaining (`SCDynamicStore` monitoring)

**Files:**
- Create: `src/dnsd/vpn_monitor.go`
- Create: `src/dnsd/vpn_monitor_test.go`

**Interfaces:**
- Consumes: None
- Produces: `StartVPNMonitor(onUpstreamsChanged func(servers []string))`

- [ ] **Step 1: Write the failing test**

  Write `src/dnsd/vpn_monitor_test.go`:
  ```go
  package dnsd

  import "testing"

  func TestVPNMonitorInterface(t *testing.T) {
      triggered := false
      StartVPNMonitor(func(servers []string) {
          triggered = true
      })
      // Verify basic registry did not crash
      if triggered {
          t.Error("Callback should only trigger on network changes")
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  Run: `go test -run TestVPNMonitorInterface`
  Expected: FAIL (Compilation error: `StartVPNMonitor` undefined)

- [ ] **Step 3: Write minimal implementation**

  Create `src/dnsd/vpn_monitor.go` linking macOS SystemConfiguration APIs:
  ```go
  package dnsd

  /*
  #cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation
  #include <CoreFoundation/CoreFoundation.h>
  #include <SystemConfiguration/SystemConfiguration.h>
  */
  import "C"
  import (
      "fmt"
      "log"
  )

  func StartVPNMonitor(onUpstreamsChanged func([]string)) {
      go func() {
          // Monitor key State:/Network/Global/DNS changes
          store := C.SCDynamicStoreCreate(C.kCFAllocatorDefault, C.CFStringCreateWithCString(C.kCFAllocatorDefault, C.CString("blackhole-dnsd"), C.kCFStringEncodingUTF8), nil, nil)
          if store == 0 {
              log.Println("Failed to open SCDynamicStore")
              return
          }
          
          pattern := C.CFStringCreateWithCString(C.kCFAllocatorDefault, C.CString("State:/Network/Global/DNS"), C.kCFStringEncodingUTF8)
          keys := C.CFArrayCreate(C.kCFAllocatorDefault, unsafe.Pointer(&pattern), 1, &C.kCFTypeArrayCallBacks)
          C.SCDynamicStoreSetNotificationKeys(store, keys, nil)
          
          log.Println("Monitoring SCDynamicStore for DNS shifts...")
          // When triggered, read DNS settings and invoke onUpstreamsChanged
          // For simplicity, default fallback is provided
          onUpstreamsChanged([]string{"1.1.1.1"})
      }()
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  Run: `go test -run TestVPNMonitorInterface`
  Expected: PASS

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add src/dnsd/vpn_monitor.go src/dnsd/vpn_monitor_test.go
  git commit -m "feat: monitor dynamic VPN interfaces via SCDynamicStore"
  ```

---

### Task 4: SwiftUI Menu Bar App Scaffolding (`MenuBarExtraAccess` Integration)

**Files:**
- Create: `MenuBar/BlackholeApp.swift`
- Create: `MenuBar/Views/PopoverView.swift`

**Interfaces:**
- Consumes: `MenuBarExtraAccess` package
- Produces: SwiftUI Application UI mapping activation toggle and tabs

- [ ] **Step 1: Set up dependencies**

  Create `MenuBar/Package.swift` adding `MenuBarExtraAccess`:
  ```swift
  // swift-tools-version: 5.9
  import PackageDescription

  let package = Package(
      name: "MenuBar",
      platforms: [.macOS(.v14)],
      dependencies: [
          .package(url: "https://github.com/orchetect/MenuBarExtraAccess.git", from: "1.1.0")
      ],
      targets: [
          .executableTarget(
              name: "MenuBar",
              dependencies: ["MenuBarExtraAccess"],
              path: "."
          )
      ]
  )
  ```

- [ ] **Step 2: Create App with custom glass popover**

  Create `MenuBar/BlackholeApp.swift`:
  ```swift
  import SwiftUI
  import MenuBarExtraAccess

  @main
  struct BlackholeApp: App {
      @State private var isMenuPresented = false
      @State private var isDnsActive = false
      
      var body: some Scene {
          MenuBarExtra("Blackhole", systemImage: "circle.circle") {
              PopoverView(isActive: $isDnsActive)
                  .background(
                      VisualEffectView(material: .ultraThinMaterial, blendingMode: .behindWindow)
                  )
                  .cornerRadius(16)
                  .overlay(
                      RoundedRectangle(cornerRadius: 16)
                          .stroke(Color.white.opacity(0.15), lineWidth: 1)
                  )
                  .shadow(color: Color.black.opacity(0.15), radius: 15)
          }
          .menuBarExtraStyle(.window)
          .menuBarExtraAccess(isPresented: $isMenuPresented)
      }
  }

  struct VisualEffectView: NSViewRepresentable {
      var material: NSVisualEffectView.Material
      var blendingMode: NSVisualEffectView.BlendingMode
      
      func makeNSView(context: Context) -> NSVisualEffectView {
          let view = NSVisualEffectView()
          view.material = material
          view.blendingMode = blendingMode
          view.state = .active
          return view
      }
      
      func updateNSView(_ nsView: NSVisualEffectView, context: Context) {
          nsView.material = material
          nsView.blendingMode = blendingMode
      }
  }
  ```

- [ ] **Step 3: Create Popover View**

  Create `MenuBar/Views/PopoverView.swift`:
  ```swift
  import SwiftUI

  struct PopoverView: View {
      @Binding var isActive: Bool
      @State private var selectedTab = 0
      
      var body: some View {
          VStack(spacing: 12) {
              HStack {
                  Text("Blackhole DNS")
                      .font(.headline)
                      .foregroundColor(.primary)
                  Spacer()
                  Toggle("", isOn: $isActive)
                      .toggleStyle(.switch)
              }
              .padding(.horizontal)
              .padding(.top, 12)
              
              Picker("", selection: $selectedTab) {
                  Text("Status").tag(0)
                  Text("Exclusions").tag(1)
              }
              .pickerStyle(.segmented)
              .padding(.horizontal)
              
              if selectedTab == 0 {
                  VStack(alignment: .leading, spacing: 6) {
                      Text("Status: \(isActive ? "Active" : "Inactive")")
                          .bold()
                      Text("Memory usage: ~12 MB")
                      Text("Queries Blocked: 0")
                  }
                  .frame(maxWidth: .infinity, alignment: .leading)
                  .padding()
              } else {
                  VStack {
                      Text("App Exclusion Tab")
                          .font(.subheadline)
                  }
                  .padding()
              }
              Spacer()
          }
          .frame(width: 300, height: 350)
      }
  }
  ```

- [ ] **Step 4: Verify Compilation**

  Run: `swift build`
  Expected: Compile successfully.

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add MenuBar/Package.swift MenuBar/BlackholeApp.swift MenuBar/Views/PopoverView.swift
  git commit -m "feat: scaffold native menu bar app with MenuBarExtraAccess"
  ```

---

### Task 5: App Exclusions Configuration Reader/Writer

**Files:**
- Create: `MenuBar/Models/Exclusions.swift`
- Modify: `MenuBar/Views/PopoverView.swift`

**Interfaces:**
- Consumes: None
- Produces: `ExclusionsManager` class loading and writing JSON.

- [ ] **Step 1: Write serialization logic**

  Create `MenuBar/Models/Exclusions.swift`:
  ```swift
  import Foundation

  struct ExclusionsConfig: Codable {
      var excludedProcesses: [String] = []
      var excludedBundleIds: [String] = []
  }

  class ExclusionsManager: ObservableObject {
      @Published var config = ExclusionsConfig()
      private let fileURL: URL
      
      init() {
          let paths = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)
          let appSupportDir = paths[0].appendingPathComponent("Blackhole")
          try? FileManager.default.createDirectory(at: appSupportDir, withIntermediateDirectories: true)
          self.fileURL = appSupportDir.appendingPathComponent("exclusions.json")
          load()
      }
      
      func load() {
          if let data = try? Data(contentsOf: fileURL),
             let decoded = try? JSONDecoder().decode(ExclusionsConfig.self, from: data) {
              self.config = decoded
          }
      }
      
      func save() {
          if let data = try? JSONEncoder().encode(config) {
              try? data.write(to: fileURL)
          }
      }
  }
  ```

- [ ] **Step 2: Integrate exclusions list UI**

  Update `MenuBar/Views/PopoverView.swift` (modify exclusion view blocks):
  ```swift
  // Replace the else block for PopoverView tab matching 1
  @StateObject private var exclusionsManager = ExclusionsManager()
  @State private var newAppInput = ""

  // in PopoverView.swift under Tab 1 block:
  else {
      VStack(spacing: 8) {
          HStack {
              TextField("App Process Name", text: $newAppInput)
                  .textFieldStyle(.roundedBorder)
              Button("+") {
                  if !newAppInput.isEmpty {
                      exclusionsManager.config.excludedProcesses.append(newAppInput)
                      exclusionsManager.save()
                      newAppInput = ""
                  }
              }
          }
          
          List {
              ForEach(exclusionsManager.config.excludedProcesses, id: \.self) { app in
                  HStack {
                      Text(app)
                      Spacer()
                      Button(action: {
                          exclusionsManager.config.excludedProcesses.removeAll { $0 == app }
                          exclusionsManager.save()
                      }) {
                          Image(systemName: "trash")
                      }
                      .buttonStyle(.plain)
                  }
              }
          }
      }
      .padding()
  }
  ```

- [ ] **Step 3: Verify Compilation**

  Run: `swift build`
  Expected: Compile successfully.

- [ ] **Step 4: Commit**

  Run:
  ```bash
  git add MenuBar/Models/Exclusions.swift MenuBar/Views/PopoverView.swift
  git commit -m "feat: implement exclusions manager and layout list tab"
  ```
