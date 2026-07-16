package dnsd

import (
	"net"
	"os"
	"testing"
)

func TestProcessCorrelationInactive(t *testing.T) {
	// Ephemeral ports without active sockets should fail cleanly or return empty
	name, bundleID, err := GetProcessInfoForPort(9999)
	if err == nil && (name != "" || bundleID != "") {
		t.Errorf("Expected lookup on inactive port to fail or return empty. Got name=%s, bundleID=%s", name, bundleID)
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
	procName, bundleID, err := GetProcessInfoForPort(port)
	if err != nil {
		t.Fatalf("Failed to get process info for active port %d: %v", port, err)
	}

	if procName != expectedPath {
		t.Errorf("Expected process name %q, got %q (bundleID: %q)", expectedPath, procName, bundleID)
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
	procName, bundleID, err := GetProcessInfoForPort(port)
	if err != nil {
		t.Fatalf("Failed to get process info for active port %d: %v", port, err)
	}

	if procName != expectedPath {
		t.Errorf("Expected process name %q, got %q (bundleID: %q)", expectedPath, procName, bundleID)
	}
}
