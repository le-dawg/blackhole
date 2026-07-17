package dnsd

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessCorrelationInactive(t *testing.T) {
	// Ephemeral ports without active sockets should fail cleanly or return empty
	name, bundleID, err := GetProcessInfoForPort(9999)
	if err == nil && (name != "" || bundleID != "") {
		t.Errorf("Expected lookup on inactive port to fail or return empty. Got name=%s, bundleID=%s", name, bundleID)
	}
}

func TestProcessCorrelationActiveTCP(t *testing.T) {
	// Reset lastScanTime to avoid rate-limiting from previous tests
	processScanMu.Lock()
	lastScanTime = time.Time{}
	processScanMu.Unlock()

	// Start a TCP listener on an ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on TCP port: %v", err)
	}
	defer ln.Close()

	// Get the allocated port
	addr := ln.Addr().(*net.TCPAddr)
	port := uint16(addr.Port)

	// Get the expected executable path
	expectedPath, err := os.Executable()
	if err != nil {
		t.Fatalf("Failed to get current executable path: %v", err)
	}

	// Lookup process info for our listening port
	procName, bundleID, err := GetProcessInfoForPort(port)
	if err != nil {
		t.Fatalf("Failed to get process info for active port %d: %v", port, err)
	}

	evalExpected, err := filepath.EvalSymlinks(expectedPath)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks for expected path: %v", err)
	}

	evalGot, err := filepath.EvalSymlinks(procName)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks for returned process name: %v", err)
	}

	if evalGot != evalExpected {
		t.Errorf("Expected process name %q (resolved: %q), got %q (resolved: %q) (bundleID: %q)", expectedPath, evalExpected, procName, evalGot, bundleID)
	}
}

func TestProcessCorrelationActiveUDP(t *testing.T) {
	// Reset lastScanTime to avoid rate-limiting from previous tests
	processScanMu.Lock()
	lastScanTime = time.Time{}
	processScanMu.Unlock()

	// Start a UDP listener on an ephemeral port
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("Failed to listen on UDP port: %v", err)
	}
	defer conn.Close()

	// Get the allocated port
	addr := conn.LocalAddr().(*net.UDPAddr)
	port := uint16(addr.Port)

	// Get the expected executable path
	expectedPath, err := os.Executable()
	if err != nil {
		t.Fatalf("Failed to get current executable path: %v", err)
	}

	// Lookup process info for our listening port
	procName, bundleID, err := GetProcessInfoForPort(port)
	if err != nil {
		t.Fatalf("Failed to get process info for active port %d: %v", port, err)
	}

	evalExpected, err := filepath.EvalSymlinks(expectedPath)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks for expected path: %v", err)
	}

	evalGot, err := filepath.EvalSymlinks(procName)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks for returned process name: %v", err)
	}

	if evalGot != evalExpected {
		t.Errorf("Expected process name %q (resolved: %q), got %q (resolved: %q) (bundleID: %q)", expectedPath, evalExpected, procName, evalGot, bundleID)
	}
}

func TestExtractBundleID(t *testing.T) {
	// Create a temporary directory structure mimicking an app bundle
	tempDir, err := os.MkdirTemp("", "testbundle_*.app")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	contentsDir := filepath.Join(tempDir, "Contents")
	if err := os.Mkdir(contentsDir, 0755); err != nil {
		t.Fatalf("Failed to create Contents dir: %v", err)
	}

	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.solution8.testapp</string>
</dict>
</plist>`

	plistPath := filepath.Join(contentsDir, "Info.plist")
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		t.Fatalf("Failed to write Info.plist: %v", err)
	}

	// Fake executable path inside the bundle
	execPath := filepath.Join(contentsDir, "MacOS", "testapp")

	bundleID := extractBundleID(execPath)
	expectedBundleID := "com.solution8.testapp"
	if bundleID != expectedBundleID {
		t.Errorf("Expected bundle ID %q, got %q", expectedBundleID, bundleID)
	}
}

func TestProcessCacheTTL(t *testing.T) {
	// Reset lastScanTime to avoid rate-limiting from previous tests
	processScanMu.Lock()
	lastScanTime = time.Time{}
	processScanMu.Unlock()

	// Clear any existing cache entries
	processCacheMu.Lock()
	processCache = make(map[uint16]cacheEntry)
	processCacheMu.Unlock()

	// Dynamically allocate a free ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on TCP: %v", err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	// Seed cache directly for the port
	processCacheMu.Lock()
	processCache[port] = cacheEntry{
		name:      "cached_proc",
		bundleID:  "cached_bundle",
		createdAt: time.Now(),
	}
	processCacheMu.Unlock()

	// Read and verify cache hit
	name, bundleID, err := GetProcessInfoForPort(port)
	if err != nil {
		t.Fatalf("Expected no error from cached port lookup, got %v", err)
	}
	if name != "cached_proc" || bundleID != "cached_bundle" {
		t.Errorf("Expected cached_proc and cached_bundle, got name=%q, bundleID=%q", name, bundleID)
	}

	// Expire cache manually
	processCacheMu.Lock()
	entry := processCache[port]
	entry.createdAt = time.Now().Add(-6 * time.Second)
	processCache[port] = entry
	processCacheMu.Unlock()

	// Verify that it no longer returns the cached values (since the port is inactive, it should return an error)
	_, _, err = GetProcessInfoForPort(port)
	if err == nil {
		t.Errorf("Expected query to fail after cache expiration")
	}
}

func TestBinaryPlistDecoding(t *testing.T) {
	// Create a temporary directory structure mimicking an app bundle
	tempDir, err := os.MkdirTemp("", "testbinaryplist_*.app")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	contentsDir := filepath.Join(tempDir, "Contents")
	if err := os.Mkdir(contentsDir, 0755); err != nil {
		t.Fatalf("Failed to create Contents dir: %v", err)
	}

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.solution8.binarytest</string>
</dict>
</plist>`

	xmlPlistPath := filepath.Join(contentsDir, "Info.plist")
	if err := os.WriteFile(xmlPlistPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to write Info.plist: %v", err)
	}

	// Use plutil to convert the plist to binary1 format in place
	cmd := exec.Command("/usr/bin/plutil", "-convert", "binary1", xmlPlistPath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to convert Info.plist to binary: %v", err)
	}

	// Fake executable path inside the bundle
	execPath := filepath.Join(contentsDir, "MacOS", "binarytest")

	// Call extractBundleID which should convert it back to XML and decode it
	bundleID := extractBundleID(execPath)
	expectedBundleID := "com.solution8.binarytest"
	if bundleID != expectedBundleID {
		t.Errorf("Expected bundle ID %q, got %q", expectedBundleID, bundleID)
	}
}

func TestLiteLLMArgumentDetection(t *testing.T) {
	// Try python3 first, fallback to python
	pyPath, err := exec.LookPath("python3")
	if err != nil {
		pyPath, err = exec.LookPath("python")
		if err != nil {
			t.Skip("Python is not installed, skipping TestLiteLLMArgumentDetection")
		}
	}

	cmd := exec.Command(pyPath, "-c", "import time; time.sleep(2)", "litellm")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy Python process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
	}()

	// Wait a tiny bit for the process to be fully active
	time.Sleep(100 * time.Millisecond)

	// Call the C helper wrapper to verify argument detection
	found := CheckPIDLiteLLM(cmd.Process.Pid)
	if !found {
		t.Errorf("Expected C helper to detect 'litellm' in arguments of PID %d", cmd.Process.Pid)
	}
}

func TestProcessCacheBulkPopulate(t *testing.T) {
	// Reset lastScanTime to avoid rate-limiting from previous tests
	processScanMu.Lock()
	lastScanTime = time.Time{}
	processScanMu.Unlock()

	// Start two TCP listeners on ephemeral ports
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on TCP port 1: %v", err)
	}
	defer ln1.Close()

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on TCP port 2: %v", err)
	}
	defer ln2.Close()

	port1 := uint16(ln1.Addr().(*net.TCPAddr).Port)
	port2 := uint16(ln2.Addr().(*net.TCPAddr).Port)

	// Clear the process cache completely
	processCacheMu.Lock()
	processCache = make(map[uint16]cacheEntry)
	processCacheMu.Unlock()

	// Query port 1. This should run a system scan and populate both port 1 and port 2.
	_, _, err = GetProcessInfoForPort(port1)
	if err != nil {
		t.Fatalf("Failed to get process info for port1: %v", err)
	}

	// Verify that port 2 is already in the cache!
	processCacheMu.RLock()
	entry2, found := processCache[port2]
	processCacheMu.RUnlock()

	if !found {
		t.Errorf("Expected port 2 (%d) to be bulk populated in the cache after querying port 1 (%d), but it was not found", port2, port1)
	} else if entry2.name == "" {
		t.Errorf("Expected bulk-populated cache entry for port 2 to have a valid process name, got empty string")
	}
}

func TestProcessScanRateLimiting(t *testing.T) {
	// Reset scan state
	processScanMu.Lock()
	lastScanTime = time.Time{}
	processCache = make(map[uint16]cacheEntry)
	processScanMu.Unlock()

	// Get a dynamically allocated free port (inactive)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port1 := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	// Query port 1. Since lastScanTime is zero, this must perform a scan and return error (port inactive)
	_, _, err = GetProcessInfoForPort(port1)
	if err == nil {
		t.Errorf("Expected lookup on inactive port to fail")
	}

	// Verify lastScanTime was updated
	processScanMu.Lock()
	scanTime1 := lastScanTime
	processScanMu.Unlock()
	if scanTime1.IsZero() {
		t.Fatalf("Expected lastScanTime to be updated after scan")
	}

	// Immediately query another inactive port. It should trigger the rate limit and fail instantly without scanning.
	port2 := port1 + 1
	if port2 == 0 {
		port2 = 1000
	}

	_, _, err = GetProcessInfoForPort(port2)
	if err == nil {
		t.Errorf("Expected rate-limited lookup to fail")
	}

	// Verify that lastScanTime did NOT change, meaning no new scan was run
	processScanMu.Lock()
	scanTime2 := lastScanTime
	processScanMu.Unlock()
	if !scanTime2.Equal(scanTime1) {
		t.Errorf("Expected scan to be rate-limited (lastScanTime unchanged), but scan time changed: %v -> %v", scanTime1, scanTime2)
	}
}

