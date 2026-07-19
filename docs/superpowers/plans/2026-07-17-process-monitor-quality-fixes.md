# Process Monitor Quality Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix rate-limiting, nested bundle ID resolution, test cleanup, and update report for the Process Identification Engine.

**Architecture:** Remove the global 500ms scan rate-limiting but keep serialization and per-port negative caching. Correctly resolve nested/case-insensitive application bundles by locating the last case-insensitive `.app/` path segment. Update tests to remove deprecated rate-limiting tests, remove lastScanTime references, and add test cases for nested/case-insensitive bundles.

**Tech Stack:** Go, standard library (sync, time, strings, filepath, os).

## Global Constraints

- Do not use any third-party library for the process monitor modifications.
- Maintain existing codebase patterns (errors, structures, concurrency models).
- All tests must compile and pass successfully.

---

### Task 1: Remove Scan Rate-Limiting

**Files:**
- Modify: `src/dnsd/process_monitor.go`

**Interfaces:**
- Consumes: `GetProcessInfoForPort` API.
- Produces: Updated `GetProcessInfoForPort` without `lastScanTime` check.

- [ ] **Step 1: Write the updated implementation**
  Modify `src/dnsd/process_monitor.go` to remove:
  - `lastScanTime` package variable definition.
  - The check `if !lastScanTime.IsZero() && time.Since(lastScanTime) < 500*time.Millisecond` in `GetProcessInfoForPort`.
  - The assignment `lastScanTime = time.Now()` in `GetProcessInfoForPort`.

  ```go
  // target code in process_monitor.go around line 157
  var (
  	processCache    = make(map[uint16]cacheEntry)
  	processCacheMu  sync.RWMutex
  	processScanMu   sync.Mutex // For serializing system-wide scans
  	bundleIDCache   = make(map[string]string)
  	bundleIDCacheMu sync.RWMutex
  	pidsScratch     []C.int
  	fdsScratch      []C.struct_proc_fdinfo
  )

  // target code in GetProcessInfoForPort around line 211-216
  	name, bundleID, err := getProcessInfoForPortNoCache(port)

  	processCacheMu.Lock()
  	if err != nil {
  ```

---

### Task 2: Fix Bundle ID Resolution in Nested Bundles

**Files:**
- Modify: `src/dnsd/process_monitor.go`

**Interfaces:**
- Consumes: `extractBundleID` function.
- Produces: Updated `extractBundleID` returning bundle ID from the correct inner-most/nested application bundle, case-insensitively.

- [ ] **Step 1: Write the updated implementation**
  Modify `extractBundleID` to resolve the last case-insensitive occurrence of `.app/`.

  ```go
  // target code in process_monitor.go around line 382
  	idx := strings.LastIndex(strings.ToLower(execPath), ".app/")
  	if idx == -1 {
  		return ""
  	}
  	appPath := execPath[:idx+4] // e.g. /Applications/Example.app
  ```

---

### Task 3: Clean up process_monitor_test.go and Remove Rate-Limiting references

**Files:**
- Modify: `src/dnsd/process_monitor_test.go`

**Interfaces:**
- Consumes: Go testing package.
- Produces: Updated test suite with `lastScanTime` resets and `TestProcessScanRateLimiting` removed.

- [ ] **Step 1: Write the updated test code**
  Modify `src/dnsd/process_monitor_test.go` to remove all blocks of the form:
  ```go
  	// Reset lastScanTime to avoid rate-limiting from previous tests
  	processScanMu.Lock()
  	lastScanTime = time.Time{}
  	processScanMu.Unlock()
  ```
  And completely remove the `TestProcessScanRateLimiting` function.

---

### Task 4: Add Test Cases for Nested and Case-Insensitive Application Bundles

**Files:**
- Modify: `src/dnsd/process_monitor_test.go`

**Interfaces:**
- Consumes: `extractBundleID` function.
- Produces: Test verification for nested and case-insensitive `.app` directories.

- [ ] **Step 1: Write the failing tests (TDD)**
  Add test assertions to verify that nested `.app/` directories and case-insensitive segments work.

  ```go
  func TestExtractBundleIDNestedAndCaseInsensitive(t *testing.T) {
  	// Setup temp directory structure
  	tmpDir, err := os.MkdirTemp("", "testbundle-*")
  	if err != nil {
  		t.Fatalf("Failed to create temp dir: %v", err)
  	}
  	defer os.RemoveAll(tmpDir)

  	// Scenario 1: Nested app bundle: /Parent.app/Contents/Resources/Nested.APP/Contents/MacOS/exec
  	nestedAppDir := filepath.Join(tmpDir, "Parent.app", "Contents", "Resources", "Nested.APP")
  	contentsDir := filepath.Join(nestedAppDir, "Contents")
  	if err := os.MkdirAll(filepath.Join(contentsDir, "MacOS"), 0755); err != nil {
  		t.Fatalf("Failed to create directory: %v", err)
  	}

  	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
  	<key>CFBundleIdentifier</key>
  	<string>com.solution8.nestedapp</string>
  </dict>
  </plist>`

  	plistPath := filepath.Join(contentsDir, "Info.plist")
  	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
  		t.Fatalf("Failed to write Info.plist: %v", err)
  	}

  	execPath := filepath.Join(contentsDir, "MacOS", "nestedexec")
  	bundleID := extractBundleID(execPath)
  	expectedBundleID := "com.solution8.nestedapp"
  	if bundleID != expectedBundleID {
  		t.Errorf("Expected nested bundle ID %q, got %q", expectedBundleID, bundleID)
  	}
  }
  ```

---

### Task 5: Run Verification

**Files:**
- N/A

**Interfaces:**
- N/A

- [ ] **Step 1: Execute all package tests**
  Run: `cd src && go test -v ./dnsd`
  Expected: All tests pass.

---

### Task 6: Append Report and Commit

**Files:**
- Modify: `.superpowers/sdd/task-2-report.md`

- [ ] **Step 1: Append changes and test outcome to report**
  Open `/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/.superpowers/sdd/task-2-report.md` and append a summary of the fixes applied, the rationale, and the test results.

- [ ] **Step 2: Commit all changes**
  Run:
  ```bash
  git add src/dnsd/process_monitor.go src/dnsd/process_monitor_test.go .superpowers/sdd/task-2-report.md
  git commit -m "fix(dnsd): remove scan rate-limiting, support nested & case-insensitive bundle ID lookup"
  ```
