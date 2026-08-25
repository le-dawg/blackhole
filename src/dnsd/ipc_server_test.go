package dnsd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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
	defer func() { _ = srv.Shutdown(context.Background()) }()

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

func TestStartIPCServer_FailsClosedUnixListener(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/blackhole-ipc-%d.sock", time.Now().UnixNano())
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	unixListener := listener.(*net.UnixListener)
	if err := unixListener.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	rb := NewRingBuffer(10)
	st := NewGlobalStats()

	srv, err := StartIPCServer(unixListener, rb, st)
	if err == nil {
		if srv != nil {
			_ = srv.Shutdown(context.Background())
		}
		t.Fatal("expected closed unix listener startup to fail")
	}
}

func TestIPCServer_ReportsUnexpectedServeErrors(t *testing.T) {
	listener := &MockIPCListener{
		connCh: make(chan net.Conn),
	}

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(listener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	select {
	case serveErr := <-srv.Errors():
		if !errors.Is(serveErr, net.ErrClosed) {
			t.Fatalf("expected net.ErrClosed, got %v", serveErr)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected unexpected serve error to be reported")
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
	defer func() { _ = srv.Shutdown(context.Background()) }()

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

	// 3. Test /pause with trailing composite JSON data
	resp, err = client.Post("http://dummy/pause", "application/json", strings.NewReader(`{}{"durationSeconds": 5}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for trailing data, got %d", resp.StatusCode)
	}
}

func TestIPCServer_PeerCredRejection(t *testing.T) {
	tmpFile := "/tmp/ipc_test_survival_go_sentinel.sock"
	os.Remove(tmpFile)
	l, err := net.Listen("unix", tmpFile)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	defer os.Remove(tmpFile)

	unixListener := l.(*net.UnixListener)
	// Start with an unauthorized UID
	authListener := NewAuthenticatedUnixListener(unixListener, []uint32{999999999})

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(authListener, rb, st)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", tmpFile)
			},
		},
		Timeout: 1 * time.Second,
	}

	// 1. First request with unauthorized UID is rejected by Accept() loop
	resp, err := client.Get("http://dummy/stats")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected unauthorized request to fail, got status %d", resp.StatusCode)
	}

	// 2. Authorize test runner UID on the SAME live server instance
	authListener.SetAllowedUIDs([]uint32{uint32(os.Getuid())})

	// 3. Second request to the same server succeeds with 200 OK, proving the server survived
	resp2, err := client.Get("http://dummy/stats")
	if err != nil {
		t.Fatalf("expected authorized request to succeed on surviving server: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp2.StatusCode)
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
	defer func() { _ = srv.Shutdown(context.Background()) }()

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
	defer func() { _ = srv.Shutdown(context.Background()) }()

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
