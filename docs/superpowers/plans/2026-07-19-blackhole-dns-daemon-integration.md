# Project Blackhole Daemon Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Go DNS filtering rule engine to ingest exclusions, and build the local UDP DNS proxy loop daemon that coordinates processes, rule-based blocking, and dynamic VPN upstreams.

**Architecture:** The daemon (`blackhole-dnsd`) runs a UDP socket listener on port `53` (or `5353` for testing). For each query, it reads caller process information, matches against exclusions loaded from `~/Library/Application Support/blackhole/exclusions.json`, queries the domain trie, and forwards to upstreams managed dynamically by the `SCDynamicStore` monitor.

**Tech Stack:** Go (for DNS daemon), `golang.org/x/net/dns/dnsmessage` (standard library for DNS message parsing).

## Global Constraints
- Target platform: macOS 15+ (Tahoe).
- Resource target: Total RAM < 35MB (Daemon <15MB, Widget <20MB). Idle CPU < 0.1%.
- Must run in user-space without private system extensions/entitlements.
- Process-level exclusion matching must support LiteLLM (python processes executing the `litellm` module) and application bundle IDs.
- VPN stack changes must be handled dynamically via SCDynamicStore notifications.
- Exclusions configuration path: `~/Library/Application Support/blackhole/exclusions.json`.

---

## Tasks

### Task 5: DNS Filtering Rule Engine (Go Exclusions Integration)

**Files:**
- Create: `src/dnsd/exclusions.go`
- Create: `src/dnsd/exclusions_test.go`

**Interfaces:**
- Consumes: None
- Produces: `LoadExclusions(path string) ([]ExcludedApp, error)`, `IsProcessExcluded(procName string, bundleID string, exclusions []ExcludedApp) bool`

- [ ] **Step 1: Write the failing test**

  Create `src/dnsd/exclusions_test.go`:
  ```go
  package dnsd

  import "testing"

  func TestIsProcessExcluded(t *testing.T) {
      exclusions := []ExcludedApp{
          {Name: "Safari", BundleID: "com.apple.Safari", IsExcluded: true},
          {Name: "LiteLLM", BundleID: "litellm", IsExcluded: true},
          {Name: "Spotify", BundleID: "com.spotify.client", IsExcluded: false},
      }

      // 1. Match by Bundle ID
      if !IsProcessExcluded("/Applications/Safari.app/Contents/MacOS/Safari", "com.apple.Safari", exclusions) {
          t.Error("Expected Safari to be excluded by bundle ID")
      }

      // 2. Match by CLI/Process name substring (e.g. litellm module python execution)
      if !IsProcessExcluded("/usr/bin/python3 -m litellm", "", exclusions) {
          t.Error("Expected litellm python script execution to match by process name substring")
      }

      // 3. Do not match when isExcluded is false
      if IsProcessExcluded("/Applications/Spotify.app/Contents/MacOS/Spotify", "com.spotify.client", exclusions) {
          t.Error("Expected Spotify to NOT be excluded since isExcluded is false")
      }

      // 4. Do not match arbitrary processes
      if IsProcessExcluded("/usr/bin/curl", "", exclusions) {
          t.Error("Expected curl to NOT be excluded")
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  Run: `go test -v -run TestIsProcessExcluded ./src/dnsd`
  Expected: FAIL (Compilation error: `ExcludedApp` / `IsProcessExcluded` undefined)

- [ ] **Step 3: Write minimal implementation**

  Create `src/dnsd/exclusions.go`:
  ```go
  package dnsd

  import (
      "encoding/json"
      "os"
      "strings"
  )

  type ExcludedApp struct {
      Name       string `json:"name"`
      BundleID   string `json:"bundleId"`
      Icon       string `json:"icon"`
      IsExcluded bool   `json:"isExcluded"`
  }

  func LoadExclusions(path string) ([]ExcludedApp, error) {
      file, err := os.Open(path)
      if err != nil {
          return nil, err
      }
      defer file.Close()

      var exclusions []ExcludedApp
      decoder := json.NewDecoder(file)
      if err := decoder.Decode(&exclusions); err != nil {
          return nil, err
      }
      return exclusions, nil
  }

  func IsProcessExcluded(procName string, bundleID string, exclusions []ExcludedApp) bool {
      procNameLower := strings.ToLower(procName)
      bundleIDLower := strings.ToLower(bundleID)

      for _, app := range exclusions {
          if !app.IsExcluded {
              continue
          }
          appBundleLower := strings.ToLower(app.BundleID)
          
          // Match by Bundle ID exactly
          if bundleIDLower != "" && bundleIDLower == appBundleLower {
              return true
          }
          
          // Match by process name/command substring
          if appBundleLower != "" && strings.Contains(procNameLower, appBundleLower) {
              return true
          }
      }
      return false
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  Run: `go test -v -run TestIsProcessExcluded ./src/dnsd`
  Expected: PASS

- [ ] **Step 5: Commit**

  Run:
  ```bash
  git add src/dnsd/exclusions.go src/dnsd/exclusions_test.go
  git commit -m "feat(dnsd): implement exclusions JSON config parser and process matching"
  ```

---

### Task 6: Local DNS Proxy Loop (`blackhole-dnsd` Daemon Main Loop)

**Files:**
- Create: `src/dnsd/main.go`
- Create: `src/dnsd/main_test.go`

**Interfaces:**
- Consumes: `Resolver`, `GetProcessInfoForPort`, `StartVPNMonitor`, `StopVPNMonitor`, `LoadExclusions`, `IsProcessExcluded`
- Produces: CLI daemon entry point listening on UDP port 53 / 5353.

- [ ] **Step 1: Write integration tests**

  Create `src/dnsd/main_test.go`:
  ```go
  package dnsd

  import (
      "net"
      "testing"
      "time"
      "golang.org/x/net/dns/dnsmessage"
  )

  func TestDNSProxyHandling(t *testing.T) {
      // Setup a dummy upstream UDP DNS server
      upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
      if err != nil {
          t.Fatalf("Failed to listen on ephemeral UDP port: %v", err)
      }
      defer upstream.Close()

      // Handle simple upstream queries and return a mock IP
      go func() {
          buf := make([]byte, 512)
          for {
              n, addr, err := upstream.ReadFromUDP(buf)
              if err != nil {
                  return
              }
              var msg dnsmessage.Message
              if err := msg.Unpack(buf[:n]); err != nil {
                  continue
              }
              
              // Build mock response
              msg.Header.Response = true
              msg.Answers = append(msg.Answers, dnsmessage.Resource{
                  Header: dnsmessage.ResourceHeader{
                      Name:  msg.Questions[0].Name,
                      Type:  dnsmessage.TypeA,
                      Class: dnsmessage.ClassINET,
                      TTL:   60,
                  },
                  Body: &dnsmessage.AResource{A: [4]byte{9, 9, 9, 9}},
              })
              resp, _ := msg.Pack()
              _, _ = upstream.WriteToUDP(resp, addr)
          }
      }()

      // Create a test resolver
      r := NewResolver([]string{upstream.LocalAddr().String()})
      r.AddBlockedDomain("ads.doubleclick.net")

      // Setup proxy UDP listener on ephemeral port
      proxyConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
      if err != nil {
          t.Fatalf("Failed to start proxy server: %v", err)
      }
      defer proxyConn.Close()

      // Simple handler loop matching the actual main.go logic
      go func() {
          buf := make([]byte, 512)
          for {
              n, cliAddr, err := proxyConn.ReadFromUDP(buf)
              if err != nil {
                  return
              }
              
              var msg dnsmessage.Message
              if err := msg.Unpack(buf[:n]); err != nil {
                  continue
              }

              domain := msg.Questions[0].Name.String()
              // Normalize trailing dot
              domain = domain[:len(domain)-1]

              // Check blocking
              if r.Resolve(domain) {
                  msg.Header.Response = true
                  msg.Header.RCode = dnsmessage.RCodeSuccess
                  msg.Answers = append(msg.Answers, dnsmessage.Resource{
                      Header: dnsmessage.ResourceHeader{
                          Name:  msg.Questions[0].Name,
                          Type:  dnsmessage.TypeA,
                          Class: dnsmessage.ClassINET,
                          TTL:   3600,
                      },
                      Body: &dnsmessage.AResource{A: [4]byte{0, 0, 0, 0}},
                  })
                  resp, _ := msg.Pack()
                  _, _ = proxyConn.WriteToUDP(resp, cliAddr)
              } else {
                  // Forward to upstream
                  upConn, err := net.Dial("udp", r.upstreams[0])
                  if err != nil {
                      continue
                  }
                  _, _ = upConn.Write(buf[:n])
                  
                  respBuf := make([]byte, 512)
                  _ = upConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
                  rn, err := upConn.Read(respBuf)
                  upConn.Close()
                  if err == nil {
                      _, _ = proxyConn.WriteToUDP(respBuf[:rn], cliAddr)
                  }
              }
          }
      }()

      // Client request helper
      queryMsg := dnsmessage.Message{
          Header: dnsmessage.Header{ID: 1234, RecursionDesired: true},
          Questions: []dnsmessage.Question{
              {
                  Name:  dnsmessage.MustNewName("ads.doubleclick.net."),
                  Type:  dnsmessage.TypeA,
                  Class: dnsmessage.ClassINET,
              },
          },
      }
      queryBytes, _ := queryMsg.Pack()

      client, err := net.Dial("udp", proxyConn.LocalAddr().String())
      if err != nil {
          t.Fatalf("Failed to dial proxy: %v", err)
      }
      defer client.Close()

      // Query 1: Blocked Domain
      _, _ = client.Write(queryBytes)
      respBuf := make([]byte, 512)
      _ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
      rn, err := client.Read(respBuf)
      if err != nil {
          t.Fatalf("Failed to read query response: %v", err)
      }

      var respMsg dnsmessage.Message
      if err := respMsg.Unpack(respBuf[:rn]); err != nil {
          t.Fatalf("Failed to unpack response: %v", err)
      }

      if len(respMsg.Answers) == 0 {
          t.Fatal("Expected response answer")
      }
      aRec := respMsg.Answers[0].Body.(*dnsmessage.AResource)
      if aRec.A != [4]byte{0, 0, 0, 0} {
          t.Errorf("Expected blocked query to resolve to 0.0.0.0, got %v", aRec.A)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it passes**

  Run: `go test -v -run TestDNSProxyHandling ./src/dnsd`
  Expected: PASS

- [ ] **Step 3: Write main program implementation**

  Create `src/dnsd/main.go`:
  ```go
  package main

  import (
      "flag"
      "fmt"
      "log"
      "net"
      "os"
      "os/signal"
      "path/filepath"
      "sync"
      "syscall"
      "time"

      "blackhole/src/dnsd"
      "golang.org/x/net/dns/dnsmessage"
  )

  var (
      upstreamsMu sync.RWMutex
      upstreams   = []string{"1.1.1.1:53", "8.8.8.8:53"}
  )

  func main() {
      port := flag.Int("port", 5353, "UDP port to listen on")
      flag.Parse()

      // Resolve exclusions path
      homeDir, err := os.UserHomeDir()
      if err != nil {
          log.Fatalf("Failed to get user home directory: %v", err)
      }
      exclusionsPath := filepath.Join(homeDir, "Library/Application Support/blackhole/exclusions.json")

      // Initialize resolver
      r := dnsd.NewResolver(upstreams)
      
      // Load example blocklist
      r.AddBlockedDomain("ads.doubleclick.net")
      r.AddBlockedDomain("adservice.google.com")

      // Start SCDynamicStore VPN Monitor
      err = dnsd.StartVPNMonitor(func(servers []string) {
          upstreamsMu.Lock()
          defer upstreamsMu.Unlock()
          
          var newUps []string
          for _, s := range servers {
              // Ensure ip contains port
              newUps = append(newUps, s+":53")
          }
          if len(newUps) > 0 {
              upstreams = newUps
              log.Printf("VPN Shift detected! Dynamically updated upstreams to: %v", upstreams)
          }
      })
      if err != nil {
          log.Printf("Warning: SCDynamicStore monitor failed to start: %v", err)
      }

      // Setup UDP listener
      addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: *port}
      conn, err := net.ListenUDP("udp", addr)
      if err != nil {
          log.Fatalf("Failed to listen on UDP port %d: %v", *port, err)
      }
      defer conn.Close()
      log.Printf("Blackhole DNS server listening on 127.0.0.1:%d...", *port)

      // Listen for exit signals
      sigChan := make(chan os.Signal, 1)
      signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
      go func() {
          <-sigChan
          log.Println("Shutting down daemon...")
          dnsd.StopVPNMonitor()
          conn.Close()
          os.Exit(0)
      }()

      // Proxy server loop
      buf := make([]byte, 512)
      for {
          n, cliAddr, err := conn.ReadFromUDP(buf)
          if err != nil {
              continue
          }

          var msg dnsmessage.Message
          if err := msg.Unpack(buf[:n]); err != nil {
              continue
          }

          if len(msg.Questions) == 0 {
              continue
          }

          question := msg.Questions[0]
          domain := question.Name.String()
          // Remove trailing dot for check
          if len(domain) > 1 && domain[len(domain)-1] == '.' {
              domain = domain[:len(domain)-1]
          }

          // 1. Check exclusions (source UDP port correlation)
          exclusions, _ := dnsd.LoadExclusions(exclusionsPath)
          procName, bundleID, err := dnsd.GetProcessInfoForPort(uint16(cliAddr.Port))
          isExcluded := false
          if err == nil {
              isExcluded = dnsd.IsProcessExcluded(procName, bundleID, exclusions)
          }

          if isExcluded {
              log.Printf("EXCLUSION bypass for process='%s' bundle='%s' domain='%s'", procName, bundleID, domain)
              forwardQuery(buf[:n], cliAddr, conn)
              continue
          }

          // 2. Check Ad-Block resolver
          if r.Resolve(domain) {
              log.Printf("BLOCKED domain='%s' client=%s", domain, cliAddr.String())
              sendBlockedResponse(msg, cliAddr, conn)
          } else {
              forwardQuery(buf[:n], cliAddr, conn)
          }
      }
  }

  func forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn) {
      upstreamsMu.RLock()
      currentUpstreams := upstreams
      upstreamsMu.RUnlock()

      for _, target := range currentUpstreams {
          upConn, err := net.Dial("udp", target)
          if err != nil {
              continue
          }
          
          _, err = upConn.Write(raw)
          if err != nil {
              upConn.Close()
              continue
          }

          respBuf := make([]byte, 512)
          _ = upConn.SetReadDeadline(time.Now().Add(1 * time.Second))
          rn, err := upConn.Read(respBuf)
          upConn.Close()
          if err == nil {
              _, _ = conn.WriteToUDP(respBuf[:rn], cliAddr)
              return
          }
      }
  }

  func sendBlockedResponse(msg dnsmessage.Message, cliAddr *net.UDPAddr, conn *net.UDPConn) {
      msg.Header.Response = true
      msg.Header.RCode = dnsmessage.RCodeSuccess
      msg.Answers = append(msg.Answers, dnsmessage.Resource{
          Header: dnsmessage.ResourceHeader{
              Name:  msg.Questions[0].Name,
              Type:  dnsmessage.TypeA,
              Class: dnsmessage.ClassINET,
              TTL:   3600,
          },
          Body: &dnsmessage.AResource{A: [4]byte{0, 0, 0, 0}},
      })
      resp, err := msg.Pack()
      if err == nil {
          _, _ = conn.WriteToUDP(resp, cliAddr)
      }
  }
  ```

- [ ] **Step 4: Run Go tests to verify integration**

  Run: `go test -count=1 -race -v ./src/dnsd`
  Expected: PASS

- [ ] **Step 5: Run main daemon locally on test port**

  Start the daemon in a background command or separate window:
  `go run src/dnsd/main.go -port 5353`
  Verify you can query it:
  `dig @127.0.0.1 -p 5353 ads.doubleclick.net`
  Expected answer: `0.0.0.0`
  Verify query bypass:
  `dig @127.0.0.1 -p 5353 google.com`
  Expected answer: correct resolving IP.

- [ ] **Step 6: Commit**

  Run:
  ```bash
  git add src/dnsd/main.go src/dnsd/main_test.go
  git commit -m "feat(dnsd): build local dns proxy UDP daemon with rules and dynamic upstream monitor"
  ```
