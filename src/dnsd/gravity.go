package dnsd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var DefaultLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

type GravityState struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

var (
	parserMu   sync.RWMutex
	parsersMap = make(map[string]ListParser)
)

// RegisterParserForURL allows users to inject a custom parser implementation for a specific URL prefix.
func RegisterParserForURL(urlPrefix string, p ListParser) {
	if p != nil {
		parserMu.Lock()
		parsersMap[urlPrefix] = p
		parserMu.Unlock()
	}
}

func loadStateMap(path string) map[string]GravityState {
	m := make(map[string]GravityState)
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Failed to open state map: %v", err)
		}
		return m
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&m); err != nil && err != io.EOF {
		log.Printf("Failed to decode state map: %v", err)
	}
	return m
}

func saveStateMap(path string, m map[string]GravityState) {
	tempPath := path + ".tmp"
	f, err := os.Create(tempPath)
	if err != nil {
		log.Printf("Failed to create temp state map file: %v", err)
		return
	}

	if err := json.NewEncoder(f).Encode(m); err != nil {
		log.Printf("Failed to encode state map: %v", err)
		f.Close()
		os.Remove(tempPath)
		return
	}

	if err := f.Close(); err != nil {
		log.Printf("Failed to close temp state map file: %v", err)
		os.Remove(tempPath)
		return
	}

	if err := os.Rename(tempPath, path); err != nil {
		log.Printf("Failed to rename temp state map to final path: %v", err)
		os.Remove(tempPath)
	}
}

func StartGravitySync(dir string, r *FilterEngine) {
	go func() {
		refreshGravity(dir, r)
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			refreshGravity(dir, r)
		}
	}()
}

func refreshGravity(dir string, r *FilterEngine) error {
	statePath := filepath.Join(dir, "gravity.state.json")
	stateMap := loadStateMap(statePath)

	client := &http.Client{Timeout: 30 * time.Second}

	var readers []io.Reader
	var filesToClose []*os.File

	defer func() {
		for _, f := range filesToClose {
			f.Close()
		}
	}()

	for i, url := range DefaultLists {
		cachePath := filepath.Join(dir, fmt.Sprintf("gravity-%d.cache", i))

		req, _ := http.NewRequest("GET", url, nil)
		if state, ok := stateMap[url]; ok {
			if state.ETag != "" {
				req.Header.Set("If-None-Match", state.ETag)
			}
			if state.LastModified != "" {
				req.Header.Set("If-Modified-Since", state.LastModified)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to fetch %s: %v", url, err)
			if f, err := os.Open(cachePath); err == nil {
				readers = append(readers, f)
				filesToClose = append(filesToClose, f)
			}
			continue
		}

		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			if f, err := os.Open(cachePath); err == nil {
				readers = append(readers, f)
				filesToClose = append(filesToClose, f)
			}
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			log.Printf("Failed to fetch %s, status code: %d", url, resp.StatusCode)
			if f, err := os.Open(cachePath); err == nil {
				readers = append(readers, f)
				filesToClose = append(filesToClose, f)
			}
			continue
		}

		tempCache := cachePath + ".tmp"
		f, err := os.Create(tempCache)
		if err != nil {
			log.Printf("Failed to create temp cache %s: %v", tempCache, err)
			resp.Body.Close()
			if fRead, err := os.Open(cachePath); err == nil {
				readers = append(readers, fRead)
				filesToClose = append(filesToClose, fRead)
			}
			continue
		}

		writer := bufio.NewWriter(f)
		parserMu.RLock()
		var parser ListParser = &PiHoleParser{}
		for prefix, p := range parsersMap {
			if strings.HasPrefix(url, prefix) {
				parser = p
				break
			}
		}
		parserMu.RUnlock()

		var writeErr error
		parseErr := parser.Parse(resp.Body, func(domain string) {
			if writeErr == nil {
				if _, err := writer.WriteString(domain + "\n"); err != nil {
					writeErr = err
				}
			}
		})

		if writeErr == nil && parseErr != nil {
			writeErr = parseErr
		}

		if writeErr == nil {
			if err := writer.Flush(); err != nil {
				writeErr = err
			}
		}

		if err := f.Close(); err != nil && writeErr == nil {
			writeErr = err
		}

		resp.Body.Close()

		if writeErr != nil {
			log.Printf("Error processing blocklist %s: %v", url, writeErr)
			os.Remove(tempCache)
			if fRead, err := os.Open(cachePath); err == nil {
				readers = append(readers, fRead)
				filesToClose = append(filesToClose, fRead)
			}
			continue
		}

		if err := os.Rename(tempCache, cachePath); err != nil {
			log.Printf("Failed to rename temp cache for %s: %v", url, err)
			os.Remove(tempCache)
			if fRead, err := os.Open(cachePath); err == nil {
				readers = append(readers, fRead)
				filesToClose = append(filesToClose, fRead)
			}
			continue
		}

		stateMap[url] = GravityState{
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		}

		if fRead, err := os.Open(cachePath); err == nil {
			readers = append(readers, fRead)
			filesToClose = append(filesToClose, fRead)
		}
	}

	saveStateMap(statePath, stateMap)

	if len(readers) == 0 {
		return errors.New("no gravity lists available")
	}

	multiReader := io.MultiReader(readers...)
	scanner := bufio.NewScanner(multiReader)
	newRoot := BuildTrieFromScanner(scanner)
	r.UpdateRoot(newRoot)
	return scanner.Err()
}
