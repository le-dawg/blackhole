// src/dnsd/ipc_server.go
package dnsd

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var pauseFlag int32

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
		json.NewEncoder(w).Encode(stats.Snapshot())
	})
	
	mux.HandleFunc("/queries", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rb.Snapshot())
	})
	
	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DurationSeconds int `json:"durationSeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DurationSeconds <= 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		
		atomic.StoreInt32(&pauseFlag, 1)
		time.AfterFunc(time.Duration(req.DurationSeconds)*time.Second, func() {
			atomic.StoreInt32(&pauseFlag, 0)
		})
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("IPC Server err: %v", err)
		}
	}()
	
	return srv, nil
}
