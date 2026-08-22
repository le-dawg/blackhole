// src/dnsd/ipc_server_test.go
package dnsd

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
)

func TestIPCServer(t *testing.T) {
	sockPath := "/tmp/blackhole_test.sock"
	os.Remove(sockPath)

	rb := NewRingBuffer(10)
	st := NewGlobalStats()
	srv, err := StartIPCServer(sockPath, rb, st)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer srv.Shutdown(context.Background())
	defer os.Remove(sockPath)

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Get("http://unix/stats")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
