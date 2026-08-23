// src/dnsd/ipc_server.go
package dnsd

import (
	"encoding/json"
	"errors"
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
	AllowedUIDs []uint32
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
	conn, err := l.UnixListener.Accept()
	if err != nil {
		return nil, err
	}

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, errors.New("not a unix connection")
	}

	raw, err := unixConn.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, err
	}

	var authErr error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			authErr = err
			return
		}

		allowed := false
		for _, uid := range l.AllowedUIDs {
			if cred.Uid == uid {
				allowed = true
				break
			}
		}

		if !allowed {
			authErr = ErrUnauthorizedUID
		}
	})

	if err != nil {
		conn.Close()
		return nil, ErrUnauthorizedIPC
	}
	
	if authErr != nil {
		conn.Close()
		if errors.Is(authErr, ErrUnauthorizedUID) {
			return nil, authErr
		}
		return nil, ErrUnauthorizedIPC
	}

	return conn, nil
}

var (
	pauseFlag  int32
	pauseMutex sync.Mutex
	pauseTimer *time.Timer
)

func IsPaused() bool {
	return atomic.LoadInt32(&pauseFlag) == 1
}

func StartIPCServer(listener net.Listener, rb *RingBuffer, stats *GlobalStats) (*http.Server, error) {

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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		pauseMutex.Lock()
		if pauseTimer != nil {
			pauseTimer.Stop()
		}
		if req.DurationSeconds <= 0 {
			atomic.StoreInt32(&pauseFlag, 0)
		} else {
			atomic.StoreInt32(&pauseFlag, 1)
			pauseTimer = time.AfterFunc(time.Duration(req.DurationSeconds)*time.Second, func() {
				atomic.StoreInt32(&pauseFlag, 0)
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
	go func() {
		if unixListener, ok := listener.(*net.UnixListener); ok {
			allowed := []uint32{0}
			if consoleUID := getConsoleUID(); consoleUID != 0 {
				allowed = append(allowed, consoleUID)
			}
			listener = &AuthenticatedUnixListener{UnixListener: unixListener, AllowedUIDs: allowed}
		}
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("IPC Server err: %v", err)
		}
	}()

	return srv, nil
}
