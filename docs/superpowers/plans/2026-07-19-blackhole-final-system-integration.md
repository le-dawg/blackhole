# Project Blackhole Final System Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate system DNS override hooks into the SwiftUI menu bar application, and create the LaunchAgent packaging to run the daemon automatically on user login.

**Architecture:** 
- The Menu Bar app intercepts the `isActive` state change. When active, it runs macOS `networksetup` command-line utilities (via Swift's `Process` API) to set the primary DNS resolver to `127.0.0.1` for all active interfaces. When inactive, it clears the override back to DHCP/default.
- A LaunchAgent plist is created to run the daemon executable in user-space automatically upon login, using standard macOS user configuration paths.

**Tech Stack:** Swift, Shell Scripting, Apple `launchd` plist.

## Global Constraints
- Target platform: macOS 15+ (Tahoe).
- Resource target: Total RAM < 35MB (Daemon <15MB, Widget <20MB). Idle CPU < 0.1%.
- Must run in user-space without private system extensions/entitlements.
- Process-level exclusion matching must support LiteLLM (python processes executing the `litellm` module) and application bundle IDs.
- VPN stack changes must be handled dynamically via SCDynamicStore notifications.
- LaunchAgent label: `com.solution8.blackhole.dnsd`
- Binary build output: `~/.local/bin/blackhole-dnsd`

---

## Tasks

### Task 7: Dynamic DNS Toggle System Override

**Files:**
- Modify: `MenuBar/Views/PopoverView.swift`
- Modify: `MenuBar/BlackholeApp.swift`

**Interfaces:**
- Consumes: `isActive` Binding toggles
- Produces: Execution of system commands to override and restore DNS resolvers dynamically.

- [ ] **Step 1: Write helper utility to execute shell commands in Swift**

  In `MenuBar/Views/PopoverView.swift` (or as a separate struct helper inside `PopoverView.swift`), add a function to run shell commands:
  ```swift
  func runShellCommand(_ command: String) -> String {
      let process = Process()
      let pipe = Pipe()
      
      process.standardOutput = pipe
      process.standardError = pipe
      process.arguments = ["-c", command]
      process.launchPath = "/bin/bash"
      
      do {
          try process.run()
          process.waitUntilExit()
          
          let data = pipe.fileHandleForReading.readDataToEndOfFile()
          if let output = String(data: data, encoding: .utf8) {
              return output.trimmingCharacters(in: .whitespacesAndNewlines)
          }
      } catch {
          return "Error: \(error.localizedDescription)"
      }
      return ""
  }
  ```

- [ ] **Step 2: Implement DNS set and clear helper functions**

  Add helper functions to toggle DNS override for the active Wi-Fi interface:
  ```swift
  func getActiveNetworkInterface() -> String {
      // Find default active network service
      let output = runShellCommand("networksetup -listallnetworkservices")
      let lines = output.components(separatedBy: "\n")
      // Safely default to 'Wi-Fi' if nothing is returned, or parse lines
      for line in lines {
          if line.contains("Wi-Fi") || line.contains("Ethernet") {
              return line
          }
      }
      return "Wi-Fi"
  }

  func setLocalDNS() {
      let interface = getActiveNetworkInterface()
      _ = runShellCommand("networksetup -setdnsservers \"\(interface)\" 127.0.0.1")
      logDNSStatus()
  }

  func clearLocalDNS() {
      let interface = getActiveNetworkInterface()
      _ = runShellCommand("networksetup -setdnsservers \"\(interface)\" empty")
      logDNSStatus()
  }

  func logDNSStatus() {
      let interface = getActiveNetworkInterface()
      let status = runShellCommand("networksetup -getdnsservers \"\(interface)\"")
      print("Current DNS servers on \(interface): \(status)")
  }
  ```

- [ ] **Step 3: Hook helper functions to toggle change in App level**

  Update `MenuBar/BlackholeApp.swift` to execute DNS override commands when `isDnsActive` is changed:
  ```swift
  // In BlackholeApp.swift's body scene, add an .onChange modifier:
  .onChange(of: isDnsActive) { _, newValue in
      if newValue {
          setLocalDNS()
      } else {
          clearLocalDNS()
      }
  }
  ```
  *(Note: You will need to move or share the DNS helper methods so they are accessible inside `BlackholeApp.swift` or define them globally).*

- [ ] **Step 4: Verify Compilation**

  Run: `swift build`
  Expected: Compile successfully.

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git commit -am "feat(menubar): add dynamic dns override shell commands for Wi-Fi interface"
  ```

---

### Task 8: LaunchAgent Packaging & Daemon Autostart

**Files:**
- Create: `install.sh`
- Create: `com.solution8.blackhole.dnsd.plist`

**Interfaces:**
- Consumes: Compiled Go binary
- Produces: LaunchAgent configuration daemon loaded into macOS startup engine.

- [ ] **Step 1: Create LaunchAgent property list**

  Create `com.solution8.blackhole.dnsd.plist`:
  ```xml
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
      <key>Label</key>
      <string>com.solution8.blackhole.dnsd</string>
      <key>ProgramArguments</key>
      <array>
          <string>/Users/thedawgctor/.local/bin/blackhole-dnsd</string>
          <string>-port</string>
          <string>5353</string>
      </array>
      <key>RunAtLoad</key>
      <true/>
      <key>KeepAlive</key>
      <true/>
  </dict>
  </plist>
  ```

- [ ] **Step 2: Create automated install script**

  Create `install.sh` in the project root to compile the Go binary and deploy the LaunchAgent:
  ```bash
  #!/bin/bash
  set -e

  echo "Building Go DNS daemon binary..."
  go build -o ~/.local/bin/blackhole-dnsd src/main.go

  echo "Copying LaunchAgent configuration..."
  mkdir -p ~/Library/LaunchAgents
  cp com.solution8.blackhole.dnsd.plist ~/Library/LaunchAgents/

  echo "Loading LaunchAgent into launchd..."
  # Unload if previously running
  launchctl bootout gui/$(id -u)/com.solution8.blackhole.dnsd 2>/dev/null || true
  # Load agent
  launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.solution8.blackhole.dnsd.plist

  echo "Deployment successful! Daemon is running in the background."
  ```

- [ ] **Step 3: Run installation script**

  Run:
  ```bash
  chmod +x install.sh
  ./install.sh
  ```
  Expected: Builds clean, copies plist, and registers into macOS launchd.

- [ ] **Step 4: Verify running process**

  Run: `ps aux | grep blackhole-dnsd`
  Expected: Shows `/Users/thedawgctor/.local/bin/blackhole-dnsd -port 5353` running in the background.

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add install.sh com.solution8.blackhole.dnsd.plist
  git commit -m "feat(deploy): package go daemon as a native macOS launchd LaunchAgent"
  ```
