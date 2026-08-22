// src/dnsd/ipc_server.go
package dnsd

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	pauseFlag  int32
	pauseMutex sync.Mutex
	pauseTimer *time.Timer
)

func IsPaused() bool {
	return atomic.LoadInt32(&pauseFlag) == 1
}

func StartIPCServer(sockPath string, rb *RingBuffer, stats *GlobalStats) (*http.Server, error) {
	os.Remove(sockPath)
	
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	
	// Must be 0666 so unprivileged GUI app can connect
	if err := os.Chmod(sockPath, 0666); err != nil {
		log.Printf("Warning: failed to chmod socket: %v", err)
	}

	mux := http.NewServeMux()
	
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats.Snapshot()); err != nil {
			log.Printf("IPC Encode error (stats): %v", err)
		}
	})
	
	mux.HandleFunc("/queries", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		for _, q := range rb.Snapshot() {
			if err := enc.Encode(q); err != nil {
				log.Printf("IPC Encode error (queries): %v", err)
				break
			}
		}
	})
	
	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DurationSeconds int `json:"durationSeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DurationSeconds <= 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		
		pauseMutex.Lock()
		if pauseTimer != nil {
			pauseTimer.Stop()
		}
		atomic.StoreInt32(&pauseFlag, 1)
		pauseTimer = time.AfterFunc(time.Duration(req.DurationSeconds)*time.Second, func() {
			atomic.StoreInt32(&pauseFlag, 0)
		})
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
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("IPC Server err: %v", err)
		}
	}()
	
	return srv, nil
}
