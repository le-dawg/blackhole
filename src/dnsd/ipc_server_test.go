package dnsd

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
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

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	// Start IPC Server directly with the authenticated listener
	srv, err := StartIPCServer(authListener, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", tmpFile)
			},
		},
	}

	// This request should fail at the transport level because the connection is closed
	// upon failing the peer credential check in Accept().
	_, err = client.Get("http://dummy/stats")
	if err == nil {
		t.Errorf("expected request to fail due to unauthorized UID rejection, but it succeeded")
	}
}
