package dnsd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestDaemonRunResult_ReturnsUnexpectedIPCCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to listen on udp socket: %v", err)
	}
	defer conn.Close()

	ipcErrCh := make(chan error, 1)
	ipcServer := &IPCServer{
		Server:      &http.Server{},
		serveErrors: ipcErrCh,
	}

	d := NewDaemon(DefaultConfig())
	go d.watchIPCServerErrors(ipcServer, cancel)
	go d.handleSignals(ctx, cancel, conn, ipcServer)

	ipcErrCh <- errors.New("boom")
	close(ipcErrCh)

	d.runMessageLoop(ctx, conn, nil, nil, nil, nil)

	err = daemonRunResult(ctx)
	if !errors.Is(err, ErrUnexpectedIPCServerTermination) {
		t.Fatalf("expected ErrUnexpectedIPCServerTermination, got %v", err)
	}
}

func TestDaemonRunResult_IgnoresExpectedShutdown(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to listen on udp socket: %v", err)
	}
	defer conn.Close()

	d := NewDaemon(DefaultConfig())
	go d.handleSignals(ctx, cancel, conn, nil)

	cancel(nil)
	d.runMessageLoop(ctx, conn, nil, nil, nil, nil)

	if err := daemonRunResult(ctx); err != nil {
		t.Fatalf("expected nil error on expected shutdown, got %v", err)
	}
}
