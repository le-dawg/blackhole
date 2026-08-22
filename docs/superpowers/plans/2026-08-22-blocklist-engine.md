# Blocklist Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a multi-format blocklist ingestion pipeline that downloads, caches, parses, and hot-reloads domains into the Trie with zero daemon restarts.

**Architecture:** A parser normalizes hosts/plain/ABP formats. A gravity downloader manages HTTP fetch, caching, and conditional refresh. User lists (whitelist/blacklist) and exclusions use `fsnotify` for hot-reloading. `RWMutex` locks protect live queries during updates. The C-helper for CLI arguments is made dynamically configurable.

**Tech Stack:** Go 1.22+, `github.com/fsnotify/fsnotify`, macOS `SCDynamicStore`, `libproc`

## Global Constraints
- Platform: macOS 15+
- Daemon RAM budget: < 15 MB total (Trie + cache combined)
- Zero restarts on list update or exclusion change
- All file paths relative to `~/Library/Application Support/blackhole/`

---

### Task 1: Multi-Format Blocklist Parser

**Files:**
- Create: `src/dnsd/blocklist_parser.go`
- Create: `src/dnsd/blocklist_parser_test.go`

**Interfaces:**
- Produces: `func ParseBlocklist(r io.Reader, onDomain func(string)) error`

- [ ] **Step 1: Write the failing test**

```go
package dnsd

import (
	"strings"
	"testing"
)

func TestParseBlocklist(t *testing.T) {
	input := `
# A comment
! Another comment
0.0.0.0 hosts.example.com
127.0.0.1 loopback.example.com
plain.example.com # inline comment
||adguard.example.com^
  spaces.example.com  
192.168.1.1 ignored.example.com
||^
`
	expected := []string{
		"hosts.example.com",
		"loopback.example.com",
		"plain.example.com",
		"adguard.example.com",
		"spaces.example.com",
	}

	var result []string
	err := ParseBlocklist(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d domains, got %d. Result: %v", len(expected), len(result), result)
	}
	for i, domain := range expected {
		if result[i] != domain {
			t.Errorf("expected %s, got %s", domain, result[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/dnsd && go test -run TestParseBlocklist -v`
Expected: FAIL with "undefined: ParseBlocklist"

- [ ] **Step 3: Write minimal implementation**

```go
package dnsd

import (
	"bufio"
	"io"
	"strings"
)

func ParseBlocklist(r io.Reader) []string {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}

		// Handle ABP/AdGuard syntax
		if strings.HasPrefix(line, "||") && strings.HasSuffix(line, "^") {
			line = line[2 : len(line)-1]
		}

		// Handle hosts file format
		fields := strings.Fields(line)
		if len(fields) > 1 {
			if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" {
				line = fields[1]
			} else {
				continue // Skip mapping to other IPs
			}
		}

		line = strings.ToLower(line)
		line = strings.TrimSuffix(line, ".")
		domains = append(domains, line)
	}
	return domains
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd src/dnsd && go test -run TestParseBlocklist -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/dnsd/blocklist_parser.go src/dnsd/blocklist_parser_test.go
git commit -m "feat: add multi-format blocklist parser"
```

---

### Task 2: User List Hot-Reloading

**Files:**
- Create: `src/dnsd/userlist.go`
- Create: `src/dnsd/userlist_test.go`
- Modify: `src/dnsd/resolver.go` (Add Whitelist checking)

**Interfaces:**
- Consumes: `github.com/fsnotify/fsnotify`
- Produces: `type UserLists struct`, `func StartUserListWatcher(dir string, r *Resolver) (*UserLists, error)`

- [ ] **Step 1: Extend Resolver for Whitelisting**

```go
// Add to src/dnsd/resolver.go
// Before: type Resolver struct { blockedDomains *Trie }
// After:
type Resolver struct {
	blockedDomains *Trie
	whitelist      map[string]bool
	blacklist      map[string]bool
	mu             sync.RWMutex
}

// Add to Resolver:
func (r *Resolver) SetLists(whitelist, blacklist map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.whitelist = whitelist
	r.blacklist = blacklist
}

// Modify IsBlocked in resolver.go to check whitelist/blacklist:
func (r *Resolver) IsBlocked(domain string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	domain = strings.TrimSuffix(domain, ".")
	
	if r.whitelist != nil && r.whitelist[domain] {
		return false
	}
	if r.blacklist != nil && r.blacklist[domain] {
		return true
	}
	
	return r.blockedDomains.Search(domain)
}
```

- [ ] **Step 2: Add fsnotify dependency**

Run: `cd src && go get github.com/fsnotify/fsnotify`

- [ ] **Step 3: Write failing test for userlist**

```go
package dnsd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserListWatcher(t *testing.T) {
	dir := t.TempDir()
	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	os.WriteFile(wlPath, []byte("good.com\n"), 0644)
	os.WriteFile(blPath, []byte("bad.com\n"), 0644)

	res := NewResolver()
	watcher, err := StartUserListWatcher(dir, res)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	time.Sleep(100 * time.Millisecond) // Allow initial load

	if res.IsBlocked("good.com") {
		t.Error("good.com should be allowed")
	}
	if !res.IsBlocked("bad.com") {
		t.Error("bad.com should be blocked")
	}

	// Test hot reload
	os.WriteFile(wlPath, []byte("good.com\nnewgood.com\n"), 0644)
	time.Sleep(100 * time.Millisecond)

	if res.IsBlocked("newgood.com") {
		t.Error("newgood.com should be allowed after reload")
	}
}
```

- [ ] **Step 4: Implement minimal UserListWatcher**

```go
package dnsd

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type UserLists struct {
	watcher *fsnotify.Watcher
}

func StartUserListWatcher(dir string, r *Resolver) (*UserLists, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	reload := func() {
		wl := loadList(wlPath)
		bl := loadList(blPath)
		r.SetLists(wl, bl)
	}

	reload()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					if event.Name == wlPath || event.Name == blPath {
						reload()
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	watcher.Add(dir)
	return &UserLists{watcher: watcher}, nil
}

func (ul *UserLists) Close() error {
	return ul.watcher.Close()
}

func loadList(path string) map[string]bool {
	m := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = true
		}
	}
	return m
}
```

- [ ] **Step 5: Run tests and commit**

Run: `cd src/dnsd && go test -run TestUserListWatcher -v`
Expected: PASS

```bash
git add src/go.mod src/go.sum src/dnsd/resolver.go src/dnsd/userlist.go src/dnsd/userlist_test.go
git commit -m "feat: add userlist hot-reloading with fsnotify"
```

---

### Task 3: Gravity Downloader & Cache

**Files:**
- Create: `src/dnsd/gravity.go`
- Create: `src/dnsd/gravity_test.go`
- Modify: `src/main.go`

**Interfaces:**
- Consumes: `ParseBlocklist()`
- Produces: `func StartGravitySync(dir string, r *Resolver)`

- [ ] **Step 1: Write the failing test**

```go
package dnsd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGravitySync(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 ads.test.com\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL} // Override for test

	res := NewResolver()
	
	err := refreshGravity(dir, res)
	if err != nil {
		t.Fatal(err)
	}

	if !res.IsBlocked("ads.test.com") {
		t.Error("ads.test.com should be blocked after gravity sync")
	}
	
	if _, err := os.Stat(filepath.Join(dir, "gravity.cache")); os.IsNotExist(err) {
		t.Error("gravity.cache should have been created")
	}
}
```

- [ ] **Step 2: Implement Gravity Downloader**

```go
package dnsd

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var DefaultLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

func StartGravitySync(dir string, r *Resolver) {
	go func() {
		refreshGravity(dir, r)
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			refreshGravity(dir, r)
		}
	}()
}

func refreshGravity(dir string, r *Resolver) error {
	cachePath := filepath.Join(dir, "gravity.cache")
	
	// Fast path: load from cache if < 24h old
	if stat, err := os.Stat(cachePath); err == nil {
		if time.Since(stat.ModTime()) < 24*time.Hour {
			return loadCache(cachePath, r)
		}
	}

	tempCache := cachePath + ".tmp"
	f, err := os.Create(tempCache)
	if err != nil {
		return loadCache(cachePath, r) // Fallback to stale cache
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var allDomains []string

	for _, url := range DefaultLists {
		resp, err := client.Get(url)
		if err != nil || resp.StatusCode != 200 {
			log.Printf("Failed to fetch %s: %v", url, err)
			continue
		}
		
		domains := ParseBlocklist(resp.Body)
		allDomains = append(allDomains, domains...)
		resp.Body.Close()
	}

	// Write cache
	writer := bufio.NewWriter(f)
	for _, d := range allDomains {
		writer.WriteString(d + "\n")
	}
	writer.Flush()
	f.Close()
	os.Rename(tempCache, cachePath)

	return loadCache(cachePath, r)
}

func loadCache(cachePath string, r *Resolver) error {
	f, err := os.Open(cachePath)
	if err != nil {
		return err
	}
	defer f.Close()

	newTrie := NewTrie()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			newTrie.Add(line)
		}
	}

	r.mu.Lock()
	r.blockedDomains = newTrie
	r.mu.Unlock()
	return nil
}
```

- [ ] **Step 3: Run tests**

Run: `cd src/dnsd && go test -run TestGravitySync -v`
Expected: PASS

- [ ] **Step 4: Wire in main.go**

Modify `src/main.go`. Remove the hardcoded `resolver.AddBlockedDomain(...)` calls. Add gravity init:

```go
// Replace:
// resolver.AddBlockedDomain("ads.doubleclick.net")
// resolver.AddBlockedDomain("adservice.google.com")

// With:
dnsd.StartGravitySync(*exclusionsPath, resolver) // Using the directory of exclusionsPath
userLists, err := dnsd.StartUserListWatcher(filepath.Dir(*exclusionsPath), resolver)
if err != nil {
    log.Printf("Warning: failed to start user list watcher: %v", err)
} else {
    defer userLists.Close()
}
```

- [ ] **Step 5: Commit**

```bash
git add src/dnsd/gravity.go src/dnsd/gravity_test.go src/main.go
git commit -m "feat: add gravity downloader, caching, and auto-refresh"
```

---

### Task 4: Exclusions File Watcher

**Files:**
- Modify: `src/dnsd/exclusions.go`
- Modify: `src/main.go`

**Interfaces:**
- Produces: `func StartExclusionWatcher(path string) (*ExclusionManager, error)`
- Produces: `func (em *ExclusionManager) IsExcluded(procName, bundleID string) bool`

- [ ] **Step 1: Rewrite Exclusions.go to use fsnotify**

```go
package dnsd

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"github.com/fsnotify/fsnotify"
)

type ExcludedApp struct {
	Name       string `json:"name"`
	BundleID   string `json:"bundleID,omitempty"`
	CliPattern string `json:"cliPattern,omitempty"`
	IsExcluded bool   `json:"isExcluded"`
}

type ExclusionManager struct {
	path       string
	exclusions []ExcludedApp
	mu         sync.RWMutex
	watcher    *fsnotify.Watcher
}

func StartExclusionWatcher(path string) (*ExclusionManager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	em := &ExclusionManager{
		path:    path,
		watcher: watcher,
	}

	em.reload()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					if event.Name == em.path {
						em.reload()
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("exclusion watcher error: %v", err)
			}
		}
	}()

	watcher.Add(path)
	return em, nil
}

func (em *ExclusionManager) reload() {
	data, err := os.ReadFile(em.path)
	if err != nil {
		return
	}
	var apps []ExcludedApp
	if err := json.Unmarshal(data, &apps); err != nil {
		return
	}
	
	em.mu.Lock()
	em.exclusions = apps
	em.mu.Unlock()
}

func (em *ExclusionManager) IsExcluded(procName, bundleID string) bool {
	em.mu.RLock()
	defer em.mu.RUnlock()

	for _, app := range em.exclusions {
		if !app.IsExcluded {
			continue
		}
		if app.BundleID != "" && app.BundleID == bundleID {
			return true
		}
		if app.CliPattern != "" && strings.Contains(procName, app.CliPattern) {
			return true
		}
		if app.Name != "" && app.Name == procName {
			return true
		}
	}
	return false
}

func (em *ExclusionManager) GetCliPatterns() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()
	
	var patterns []string
	for _, app := range em.exclusions {
		if app.IsExcluded && app.CliPattern != "" {
			patterns = append(patterns, app.CliPattern)
		}
	}
	return patterns
}

func (em *ExclusionManager) Close() error {
	return em.watcher.Close()
}
```

- [ ] **Step 2: Wire in main.go**

Modify `src/main.go` to use the new manager instead of reading per-query:

```go
// Replace:
// exclusions, err := dnsd.LoadExclusions(*exclusionsPath)
// if err != nil { log.Printf("Warning: failed to load exclusions: %v", err) }

// With:
exclusionManager, err := dnsd.StartExclusionWatcher(*exclusionsPath)
if err != nil {
	log.Fatalf("Failed to start exclusion watcher: %v", err)
}
defer exclusionManager.Close()
```

Modify the query handler:
```go
// Replace:
// if dnsd.IsProcessExcluded(procInfo.ProcessName, procInfo.BundleID, exclusions) { ... }

// With:
if exclusionManager.IsExcluded(procInfo.ProcessName, procInfo.BundleID) { ... }
```

- [ ] **Step 3: Run tests and commit**

Run: `cd src && go test ./...`
Expected: PASS (fix any `LoadExclusions` references in old tests)

```bash
git add src/dnsd/exclusions.go src/main.go
git commit -m "feat: use fsnotify for exclusions to eliminate per-query disk I/O"
```

---

### Task 5: Configurable CLI Argument Matching

**Files:**
- Modify: `src/dnsd/process_monitor.go`
- Modify: `src/dnsd/vpn_monitor.c` (C Shim rename required for purity, but functionality is here)
- Modify: `src/main.go`

**Interfaces:**
- Consumes: `exclusionManager.GetCliPatterns()`

- [ ] **Step 1: Modify C helper to accept patterns array**

In `src/dnsd/vpn_monitor.c`, update `get_process_info_for_port` to take patterns:

```c
// Before:
// int get_process_info_for_port(uint16_t port, ProcessInfo *info) {
// ...
// if (strstr(arg_ptr, "litellm") != NULL) {

// After:
int get_process_info_for_port(uint16_t port, ProcessInfo *info, const char** patterns, int pattern_count) {
// ...
if (pattern_count > 0 && patterns != NULL) {
    for (int i=0; i<pattern_count; i++) {
        if (strstr(arg_ptr, patterns[i]) != NULL) {
            strncpy(info->process_name, patterns[i], sizeof(info->process_name)-1);
            strncpy(info->bundle_id, patterns[i], sizeof(info->bundle_id)-1);
            free(proc_args);
            return 0;
        }
    }
}
```

- [ ] **Step 2: Update Go wrapper in process_monitor.go**

```go
// Replace existing GetProcessInfoForPort wrapper:
func GetProcessInfoForPort(port uint16, patterns []string) (ProcessInfo, error) {
	// ... cache check logic ...

	var info C.ProcessInfo
	
	// Convert Go []string to C array of char*
	cPatterns := make([]*C.char, len(patterns))
	for i, p := range patterns {
		cPatterns[i] = C.CString(p)
		defer C.free(unsafe.Pointer(cPatterns[i]))
	}
	
	var cPatternsPtr **C.char
	if len(cPatterns) > 0 {
		cPatternsPtr = &cPatterns[0]
	}

	ret := C.get_process_info_for_port(C.uint16_t(port), &info, cPatternsPtr, C.int(len(patterns)))
	// ... rest of function ...
}
```

- [ ] **Step 3: Update main.go to pass patterns**

```go
// In main.go query handler:
patterns := exclusionManager.GetCliPatterns()
procInfo, err := dnsd.GetProcessInfoForPort(clientPort, patterns)
```

- [ ] **Step 4: Run tests and commit**

Run: `cd src && go test ./...`
Expected: PASS

```bash
git add src/dnsd/vpn_monitor.c src/dnsd/process_monitor.go src/main.go
git commit -m "feat: make C-level CLI argument matching dynamically configurable"
```
