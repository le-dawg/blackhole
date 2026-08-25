// src/dnsd/ipc_server.go
package dnsd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrUnauthorizedUID = errors.New("unauthorized UID")
	ErrUnauthorizedIPC = errors.New("unauthorized ipc access")
)

type AuthenticatedUnixListener struct {
	*net.UnixListener
	allowedUIDsMu sync.RWMutex
	allowedUIDs   []uint32
}

func NewAuthenticatedUnixListener(unixListener *net.UnixListener, allowedUIDs []uint32) *AuthenticatedUnixListener {
	listener := &AuthenticatedUnixListener{UnixListener: unixListener}
	listener.SetAllowedUIDs(allowedUIDs)
	return listener
}

func (l *AuthenticatedUnixListener) SetAllowedUIDs(allowedUIDs []uint32) {
	l.allowedUIDsMu.Lock()
	defer l.allowedUIDsMu.Unlock()
	l.allowedUIDs = append([]uint32(nil), allowedUIDs...)
}

func (l *AuthenticatedUnixListener) isAllowedUID(uid uint32) bool {
	l.allowedUIDsMu.RLock()
	defer l.allowedUIDsMu.RUnlock()

	for _, allowedUID := range l.allowedUIDs {
		if uid == allowedUID {
			return true
		}
	}

	return false
}

func getConsoleUID() uint32 {
	info, err := os.Stat("/dev/console")
	if err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			return stat.Uid
		}
	}
	return 0
}

func (l *AuthenticatedUnixListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.UnixListener.Accept()
		if err != nil {
			return nil, err
		}

		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			continue
		}

		raw, err := unixConn.SyscallConn()
		if err != nil {
			conn.Close()
			continue
		}

		var authErr error
		err = raw.Control(func(fd uintptr) {
			cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
			if err != nil {
				authErr = err
				return
			}

				if !l.isAllowedUID(cred.Uid) {
					authErr = ErrUnauthorizedUID
				}
			})

		if err != nil || authErr != nil {
			conn.Close()
			continue
		}

		return conn, nil
	}
}

var (
	pauseFlag       int32
	pauseMutex      sync.Mutex
	pauseTimer      *time.Timer
	pauseGeneration uint64
)

type IPCServer struct {
	*http.Server
	serveErrors <-chan error
}

func (s *IPCServer) Errors() <-chan error {
	return s.serveErrors
}

func IsPaused() bool {
	return atomic.LoadInt32(&pauseFlag) == 1
}

func StartIPCServer(listener net.Listener, rb *RingBuffer, stats *GlobalStats) (*IPCServer, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats.Snapshot()); err != nil {
			log.Printf("IPC Encode error (stats): %v", err)
		}
	})

	mux.HandleFunc("/queries", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for _, q := range rb.Snapshot() {
			if err := enc.Encode(q); err != nil {
				log.Printf("IPC Encode error (queries): %v", err)
				break
			}
		}
	})

	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			DurationSeconds int `json:"durationSeconds"`
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var trailing json.RawMessage
		if err := dec.Decode(&trailing); err != io.EOF {
			http.Error(w, "invalid request: trailing data", http.StatusBadRequest)
			return
		}

		pauseMutex.Lock()
		pauseGeneration++
		currentGen := pauseGeneration
		if pauseTimer != nil {
			pauseTimer.Stop()
			pauseTimer = nil
		}
		if req.DurationSeconds <= 0 {
			atomic.StoreInt32(&pauseFlag, 0)
		} else {
			atomic.StoreInt32(&pauseFlag, 1)
			pauseTimer = time.AfterFunc(time.Duration(req.DurationSeconds)*time.Second, func() {
				pauseMutex.Lock()
				defer pauseMutex.Unlock()
				if pauseGeneration == currentGen {
					atomic.StoreInt32(&pauseFlag, 0)
				}
			})
		}
		pauseMutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			log.Printf("IPC Encode error (pause): %v", err)
		}
	})

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}
	serveErrCh := make(chan error, 1)
	ipcServer := &IPCServer{
		Server:      srv,
		serveErrors: serveErrCh,
	}

	serveListener := listener
	readySocketPath := ""
	if unixListener, ok := listener.(*net.UnixListener); ok {
		allowed := []uint32{0}
		if consoleUID := getConsoleUID(); consoleUID != 0 {
			allowed = append(allowed, consoleUID)
		}
		serveListener = NewAuthenticatedUnixListener(unixListener, allowed)
		if addr, ok := unixListener.Addr().(*net.UnixAddr); ok {
			readySocketPath = addr.Name
		}
	}

	go func() {
		if err := srv.Serve(serveListener); err != nil && err != http.ErrServerClosed {
			select {
			case serveErrCh <- err:
			default:
			}
			log.Printf("IPC Server err: %v", err)
		}
		close(serveErrCh)
	}()

	if readySocketPath != "" {
		if err := waitForIPCServerReady(readySocketPath, serveErrCh, 500*time.Millisecond); err != nil {
			_ = srv.Close()
			return nil, err
		}
	}

	return ipcServer, nil
}

func waitForIPCServerReady(socketPath string, serveErrCh <-chan error, timeout time.Duration) error {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   100 * time.Millisecond,
	}

	deadline := time.Now().Add(timeout)
	errCh := serveErrCh
	for time.Now().Before(deadline) {
		select {
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				break
			}
			if err != nil {
				return fmt.Errorf("failed to start IPC server: %w", err)
			}
		default:
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://unix/stats", nil)
		if err != nil {
			return fmt.Errorf("build IPC readiness probe request: %w", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			return fmt.Errorf("failed to start IPC server: %w", err)
		}
	default:
	}

	return fmt.Errorf("timed out waiting for IPC server readiness on %s", socketPath)
}
