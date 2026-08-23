package dnsd

import (
	"context"
	"net"
	"net/http"
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
