package dnsd

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type UserLists struct {
	watcher *fsnotify.Watcher
}

var (
	loadListFuncMu sync.RWMutex
	loadListFunc   = loadList
)

func callLoadList(path string) (map[string]bool, error) {
	loadListFuncMu.RLock()
	f := loadListFunc
	loadListFuncMu.RUnlock()
	return f(path)
}

func setLoadListFuncForTest(f func(string) (map[string]bool, error)) {
	loadListFuncMu.Lock()
	loadListFunc = f
	loadListFuncMu.Unlock()
}

func StartUserListWatcher(dir string, r *FilterEngine) (*UserLists, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	lastWhitelist := map[string]bool{}
	lastBlacklist := map[string]bool{}
	var reloadMu sync.Mutex

	reload := func() {
		reloadMu.Lock()
		defer reloadMu.Unlock()

		wl, err := callLoadList(wlPath)
		if err != nil {
			log.Printf("userlist whitelist reload error: %v", err)
		} else {
			lastWhitelist = wl
		}

		bl, err := callLoadList(blPath)
		if err != nil {
			log.Printf("userlist blacklist reload error: %v", err)
		} else {
			lastBlacklist = bl
		}

		r.SetLists(lastWhitelist, lastBlacklist)
	}

	reload()

	go func() {
		var timer *time.Timer
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Name == wlPath || event.Name == blPath {
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(50*time.Millisecond, reload)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, err
	}
	return &UserLists{watcher: watcher}, nil
}

func (ul *UserLists) Close() error {
	return ul.watcher.Close()
}

func loadList(path string) (map[string]bool, error) {
	m := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			line = strings.TrimSuffix(strings.ToLower(line), ".")
			m[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return m, nil
}
