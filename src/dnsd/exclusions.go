package dnsd

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type ExcludedApp struct {
	Name       string `json:"name"`
	BundleID   string `json:"bundleId,omitempty"`
	CliPattern string `json:"cliPattern,omitempty"`
	IsExcluded bool   `json:"isExcluded"`
}

type ExclusionManager struct {
	path          string
	exclusions    []ExcludedApp
	mu            sync.RWMutex
	watcher       *fsnotify.Watcher
	closeChan     chan struct{}
	closeOnce     sync.Once
	debounceTimer *time.Timer
	timerMu       sync.Mutex
}

func StartExclusionWatcher(path string) (*ExclusionManager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	em := &ExclusionManager{
		path:      path,
		watcher:   watcher,
		closeChan: make(chan struct{}),
	}

	em.reload()

	// Watch the directory, not the file itself, to handle atomic saves (Rename/Remove)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		watcher.Close()
		return nil, err
	}
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, err
	}

	go func() {
		for {
			select {
			case <-em.closeChan:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Filter events for our specific file
				if filepath.Clean(event.Name) == filepath.Clean(em.path) {
					em.timerMu.Lock()
					select {
					case <-em.closeChan:
						em.timerMu.Unlock()
						return
					default:
						if em.debounceTimer != nil {
							em.debounceTimer.Stop()
						}
						em.debounceTimer = time.AfterFunc(50*time.Millisecond, func() {
							em.reload()
						})
					}
					em.timerMu.Unlock()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("exclusion watcher error: %v", err)
			}
		}
	}()

	return em, nil
}

func (em *ExclusionManager) reload() {
	data, err := os.ReadFile(em.path)
	if err != nil {
		return
	}
	var apps []ExcludedApp
	if err := json.Unmarshal(data, &apps); err != nil {
		return
	}

	em.mu.Lock()
	em.exclusions = apps
	em.mu.Unlock()
}

func (em *ExclusionManager) IsExcluded(procName, bundleID string) bool {
	em.mu.RLock()
	defer em.mu.RUnlock()

	for _, app := range em.exclusions {
		if !app.IsExcluded {
			continue
		}
		if app.BundleID != "" && strings.EqualFold(app.BundleID, bundleID) {
			return true
		}
		if app.CliPattern != "" && strings.Contains(strings.ToLower(procName), strings.ToLower(app.CliPattern)) {
			return true
		}
		if app.Name != "" && strings.EqualFold(app.Name, procName) {
			return true
		}
	}
	return false
}

func (em *ExclusionManager) GetCliPatterns() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()

	var patterns []string
	for _, app := range em.exclusions {
		if app.IsExcluded && app.CliPattern != "" {
			patterns = append(patterns, app.CliPattern)
		}
	}
	return patterns
}

func (em *ExclusionManager) Close() error {
	var err error
	em.closeOnce.Do(func() {
		close(em.closeChan)
		em.timerMu.Lock()
		if em.debounceTimer != nil {
			em.debounceTimer.Stop()
			em.debounceTimer = nil
		}
		em.timerMu.Unlock()
		err = em.watcher.Close()
	})
	return err
}
