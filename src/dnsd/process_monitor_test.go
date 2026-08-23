package dnsd

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	go StartProcessMonitor(context.Background())
	os.Exit(m.Run())
}

func TestProcessCorrelationInactive(t *testing.T) {
	// Ephemeral ports without active sockets should fail cleanly and return "Unknown"
	_, _, _ = GetProcessInfoForPort(9999, []string{"litellm"})
	WaitForScan()
	name, bundleID, err := GetProcessInfoForPort(9999, []string{"litellm"})
	if name != "Unknown" || bundleID != "" {
		t.Errorf("Expected lookup on inactive port to return 'Unknown' and empty bundleID. Got name=%s, bundleID=%s, err=%v", name, bundleID, err)
	}
}

func TestProcessCorrelationActiveTCP(t *testing.T) {
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
	_, _, _ = GetProcessInfoForPort(port, []string{"litellm"})
	WaitForScan()
	procName, bundleID, err := GetProcessInfoForPort(port, []string{"litellm"})
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
	_, _, _ = GetProcessInfoForPort(port, []string{"litellm"})
	WaitForScan()
	procName, bundleID, err := GetProcessInfoForPort(port, []string{"litellm"})
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
	<string>com.blackhole.testapp</string>
</dict>
</plist>`

	plistPath := filepath.Join(contentsDir, "Info.plist")
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		t.Fatalf("Failed to write Info.plist: %v", err)
	}

	// Fake executable path inside the bundle
	execPath := filepath.Join(contentsDir, "MacOS", "testapp")

	bundleID := extractBundleID(execPath)
	expectedBundleID := "com.blackhole.testapp"
	if bundleID != expectedBundleID {
		t.Errorf("Expected bundle ID %q, got %q", expectedBundleID, bundleID)
	}
}

func TestProcessCacheTTL(t *testing.T) {
	// Clear any existing cache entries
	portToPIDCache.Store(&PortCache{Mappings: make(map[uint16]portPIDEntry)})

	pidMetadataCacheMu.Lock()
	pidMetadataCache = make(map[int]pidMetadataEntry)
	pidMetadataCacheMu.Unlock()

	// Dynamically allocate a free ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on TCP: %v", err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	oldCache := portToPIDCache.Load()
	newMappings := make(map[uint16]portPIDEntry)
	for k, v := range oldCache.Mappings {
		newMappings[k] = v
	}
	newMappings[port] = portPIDEntry{
		pid:       12345,
		createdAt: time.Now(),
	}
	portToPIDCache.Store(&PortCache{Mappings: newMappings})

	pidMetadataCacheMu.Lock()
	pidMetadataCache[12345] = pidMetadataEntry{
		metadata: ProcessMetadata{
			Name:     "cached_proc",
			BundleID: "cached_bundle",
		},
		createdAt: time.Now(),
	}
	pidMetadataCacheMu.Unlock()

	// Read and verify cache hit
	name, bundleID, err := GetProcessInfoForPort(port, []string{"litellm"})
	if err != nil {
		t.Fatalf("Expected no error from cached port lookup, got %v", err)
	}
	if name != "cached_proc" || bundleID != "cached_bundle" {
		t.Errorf("Expected cached_proc and cached_bundle, got name=%q, bundleID=%q", name, bundleID)
	}

	// Expire cache manually
	oldCache2 := portToPIDCache.Load()
	newMappings2 := make(map[uint16]portPIDEntry)
	for k, v := range oldCache2.Mappings {
		newMappings2[k] = v
	}
	pEntry := newMappings2[port]
	pEntry.createdAt = time.Now().Add(-6 * time.Second)
	newMappings2[port] = pEntry
	portToPIDCache.Store(&PortCache{Mappings: newMappings2})

	pidMetadataCacheMu.Lock()
	mEntry := pidMetadataCache[12345]
	mEntry.createdAt = time.Now().Add(-65 * time.Second)
	pidMetadataCache[12345] = mEntry
	pidMetadataCacheMu.Unlock()

	// Verify that it no longer returns the cached values (since the port is inactive, it should return an error)
	_, _, _ = GetProcessInfoForPort(port, []string{"litellm"})
	WaitForScan()
	_, _, err = GetProcessInfoForPort(port, []string{"litellm"})
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
	<string>com.blackhole.binarytest</string>
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
	expectedBundleID := "com.blackhole.binarytest"
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
	found := CheckPIDPatterns(cmd.Process.Pid, []string{"litellm"})
	if found == "" {
		t.Errorf("Expected C helper to detect 'litellm' in arguments of PID %d", cmd.Process.Pid)
	}
}

func TestProcessCacheBulkPopulate(t *testing.T) {
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

	// Clear the portToPIDCache completely
	portToPIDCache.Store(&PortCache{Mappings: make(map[uint16]portPIDEntry)})

	// Query port 1. This should run a system scan and populate both port 1 and port 2.
	_, _, err = GetProcessInfoForPort(port1, []string{"litellm"})
	WaitForScan()
	_, _, err = GetProcessInfoForPort(port1, []string{"litellm"})
	if err != nil {
		t.Fatalf("Failed to get process info for port1: %v", err)
	}

	// Verify that port 2 is already in the portToPIDCache!
	c := portToPIDCache.Load()
	entry2, found := c.Mappings[port2]

	if !found {
		t.Errorf("Expected port 2 (%d) to be bulk populated in the portToPIDCache after querying port 1 (%d), but it was not found", port2, port1)
	} else if entry2.pid <= 0 {
		t.Errorf("Expected bulk-populated portToPIDCache entry for port 2 to have a valid PID, got %d", entry2.pid)
	}
}

func TestExtractBundleIDNestedAndCaseInsensitive(t *testing.T) {
	// Setup temp directory structure
	tmpDir, err := os.MkdirTemp("", "testbundle-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Scenario 1: Nested app bundle with case insensitivity: /Parent.app/Contents/Resources/Nested.APP/Contents/MacOS/exec
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
	<string>com.blackhole.nestedapp</string>
</dict>
</plist>`

	plistPath := filepath.Join(contentsDir, "Info.plist")
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		t.Fatalf("Failed to write Info.plist: %v", err)
	}

	execPath := filepath.Join(contentsDir, "MacOS", "nestedexec")
	bundleID := extractBundleID(execPath)
	expectedBundleID := "com.blackhole.nestedapp"
	if bundleID != expectedBundleID {
		t.Errorf("Expected nested bundle ID %q, got %q", expectedBundleID, bundleID)
	}
}


func TestStartProcessMonitor_RestartAndCancellation(t *testing.T) {
	// Start with context 1
	ctx1, cancel1 := context.WithCancel(context.Background())
	go StartProcessMonitor(ctx1)
	
	// Let workers start
	time.Sleep(50 * time.Millisecond)
	
	if count := ActiveWorkersCount(); count != 2 {
		t.Fatalf("Expected 2 active workers, got %d", count)
	}
	
	// Create some artificial load by firing queries that trigger scanTasks
	for i := 0; i < 50; i++ {
		go GetProcessInfoForPort(uint16(10000+i), []string{"dummy"})
	}

	// Wait briefly to allow processing
	time.Sleep(50 * time.Millisecond)

	// Start with context 2, which should wait for ctx1 workers to cleanly shut down
	ctx2, cancel2 := context.WithCancel(context.Background())
	
	monitorDone := make(chan struct{})
	go func() {
		StartProcessMonitor(ctx2)
		
		// Context 1 should have been cancelled by the new StartProcessMonitor call,
		// and new workers launched. Let's verify zero accumulation.
		if count := ActiveWorkersCount(); count != 2 {
			t.Errorf("Expected 2 active workers after restart, got %d", count)
		}
		
		close(monitorDone)
	}()

	<-monitorDone // wait for start

	// Create some load for ctx2
	for i := 0; i < 50; i++ {
		go GetProcessInfoForPort(uint16(20000+i), []string{"dummy"})
	}

	cancel1() // cancel1 should be a no-op as it was cancelled inside StartProcessMonitor
	cancel2()
	
	// Explicit teardown
	processMonitorMu.Lock()
	if processMonitorCancel != nil {
		processMonitorCancel()
	}
	processMonitorWg.Wait()
	processMonitorMu.Unlock()
	
	if count := ActiveWorkersCount(); count != 0 {
		t.Fatalf("Expected 0 active workers after cancellation and wait, got %d", count)
	}

	// Restart so subsequent tests pass
	go StartProcessMonitor(context.Background())
}
