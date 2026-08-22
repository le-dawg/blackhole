package dnsd

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type UserLists struct {
	watcher *fsnotify.Watcher
}

func StartUserListWatcher(dir string, r *Resolver) (*UserLists, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	reload := func() {
		wl := loadList(wlPath)
		bl := loadList(blPath)
		r.SetLists(wl, bl)
	}

	reload()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					if event.Name == wlPath || event.Name == blPath {
						reload()
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	watcher.Add(dir)
	return &UserLists{watcher: watcher}, nil
}

func (ul *UserLists) Close() error {
	return ul.watcher.Close()
}

func loadList(path string) map[string]bool {
	m := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = true
		}
	}
	return m
}
