package dnsd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
func TestIPCServer(t *testing.T) {
	// Use net.Pipe for mock listener
	clientConn, serverConn := net.Pipe()

	listener := &MockIPCListener{
		connCh: make(chan net.Conn, 1),
	}
	listener.connCh <- serverConn

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(listener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return clientConn, nil
			},
		},
	}

	resp, err := client.Get("http://dummy/stats")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

type MockIPCListener struct {
	connCh chan net.Conn
	closed bool
}

func (m *MockIPCListener) Accept() (net.Conn, error) {
	conn, ok := <-m.connCh
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (m *MockIPCListener) Close() error {
	if !m.closed {
		m.closed = true
		close(m.connCh)
	}
	return nil
}

func (m *MockIPCListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "mock", Net: "unix"}
}

func TestIPCServer_PauseEndpoint(t *testing.T) {
	// A new connection is needed per request with net.Pipe
	listener := &MockIPCListener{
		connCh: make(chan net.Conn, 10),
	}

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(listener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				listener.connCh <- serverConn
				return clientConn, nil
			},
		},
	}

	// 1. Test /pause with invalid JSON
	resp, err := client.Post("http://dummy/pause", "application/json", strings.NewReader("{invalid-json"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// 2. Test /pause with valid JSON
	resp, err = client.Post("http://dummy/pause", "application/json", strings.NewReader(`{"durationSeconds": 5}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestIPCServer_PeerCredRejection(t *testing.T) {
	tmpFile := filepath.Join("/tmp", "ipc_test.sock")
	l, err := net.Listen("unix", tmpFile)
	if err != nil {
		t.Fatalf("failed to listen on unix socket: %v", err)
	}
	defer l.Close()

	unixListener, ok := l.(*net.UnixListener)
	if !ok {
		t.Fatalf("expected *net.UnixListener")
	}

	// Create AuthenticatedUnixListener with an impossible UID to force auth rejection
	authListener := &AuthenticatedUnixListener{
		UnixListener: unixListener,
		AllowedUIDs:  []uint32{999999999}, // Assumes this UID does not match the test runner
	}

	// Connect to the socket in the background to trigger Accept()
	go func() {
		conn, err := net.Dial("unix", tmpFile)
		if err == nil {
			conn.Close()
		}
	}()

	// Accept the connection, which should fail due to peer-credential rejection
	_, err = authListener.Accept()
	if err == nil {
		t.Fatalf("expected Accept to fail due to unauthorized UID rejection, but it succeeded")
	}

	// Explicitly prove the peer-credential auth branch fired using errors.Is
	if !errors.Is(err, ErrUnauthorizedUID) {
		t.Errorf("expected error ErrUnauthorizedUID, got: %v", err)
	}
}

func TestIPCServer_QueriesEndpoint(t *testing.T) {
	listener := &MockIPCListener{
		connCh: make(chan net.Conn, 10),
	}

	rb := NewRingBuffer(10)
	
	now := time.Now().Truncate(time.Second).UTC()
	record1 := QueryRecord{Timestamp: now, Domain: "blocked.com", QueryType: 1, Status: "Blocked", ProcessName: "curl", BundleID: "com.apple.curl", LatencyMs: 10.5}
	record2 := QueryRecord{Timestamp: now.Add(time.Second), Domain: "allowed.com", QueryType: 28, Status: "Allowed", ProcessName: "safari", BundleID: "com.apple.Safari", LatencyMs: 15.2}
	record3 := QueryRecord{Timestamp: now.Add(2 * time.Second), Domain: "excluded.com", QueryType: 1, Status: "Excluded", ProcessName: "chrome", BundleID: "com.google.Chrome", LatencyMs: 5.0}

	rb.Push(record1)
	rb.Push(record2)
	rb.Push(record3)

	st := NewGlobalStats()
	srv, err := StartIPCServer(listener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				listener.connCh <- serverConn
				return clientConn, nil
			},
		},
	}

	resp, err := client.Post("http://dummy/queries", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", resp.StatusCode)
	}

	resp2, err := client.Get("http://dummy/queries")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp2.StatusCode)
	}

	if ct := resp2.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("expected Content-Type application/x-ndjson, got %s", ct)
	}

	decoder := json.NewDecoder(resp2.Body)
	var records []QueryRecord
	for {
		var rec QueryRecord
		if err := decoder.Decode(&rec); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("failed to decode: %v", err)
		}
		records = append(records, rec)
	}

	expectedRecords := []QueryRecord{record1, record2, record3}
	if len(records) != len(expectedRecords) {
		t.Fatalf("expected %d records, got %d", len(expectedRecords), len(records))
	}

	for i, expected := range expectedRecords {
		if !records[i].Timestamp.Equal(expected.Timestamp) {
			t.Errorf("record %d: expected Timestamp %v, got %v", i, expected.Timestamp, records[i].Timestamp)
		}
		if records[i].Domain != expected.Domain {
			t.Errorf("record %d: expected Domain %v, got %v", i, expected.Domain, records[i].Domain)
		}
		if records[i].QueryType != expected.QueryType {
			t.Errorf("record %d: expected QueryType %v, got %v", i, expected.QueryType, records[i].QueryType)
		}
		if records[i].Status != expected.Status {
			t.Errorf("record %d: expected Status %v, got %v", i, expected.Status, records[i].Status)
		}
		if records[i].ProcessName != expected.ProcessName {
			t.Errorf("record %d: expected ProcessName %v, got %v", i, expected.ProcessName, records[i].ProcessName)
		}
		if records[i].BundleID != expected.BundleID {
			t.Errorf("record %d: expected BundleID %v, got %v", i, expected.BundleID, records[i].BundleID)
		}
		if records[i].LatencyMs != expected.LatencyMs {
			t.Errorf("record %d: expected LatencyMs %v, got %v", i, expected.LatencyMs, records[i].LatencyMs)
		}
	}
}
func TestIPCServer_PauseOverlappingGenerationRace(t *testing.T) {
	listener := &MockIPCListener{
		connCh: make(chan net.Conn, 10),
	}
	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(listener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				listener.connCh <- serverConn
				return clientConn, nil
			},
		},
	}

	// Wait helper
	wait := func(d time.Duration) { time.Sleep(d) }

	// Send POST /pause with durationSeconds: 1
	resp, err := client.Post("http://dummy/pause", "application/json", strings.NewReader(`{"durationSeconds": 1}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if !IsPaused() {
		t.Fatalf("expected paused to be true")
	}

	// Wait 100ms and send POST /pause with durationSeconds: 3
	wait(100 * time.Millisecond)
	resp, err = client.Post("http://dummy/pause", "application/json", strings.NewReader(`{"durationSeconds": 3}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if !IsPaused() {
		t.Fatalf("expected paused to be true")
	}

	// Wait 1.1s (past the initial 1s timer expiration)
	wait(1100 * time.Millisecond)
	if !IsPaused() {
		t.Fatalf("expected paused to be true after 1.2s total (proving first timer did not unpause)")
	}

	// Send POST /pause with durationSeconds: 0
	resp, err = client.Post("http://dummy/pause", "application/json", strings.NewReader(`{"durationSeconds": 0}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if IsPaused() {
		t.Fatalf("expected paused to be false")
	}
}
