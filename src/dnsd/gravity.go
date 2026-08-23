package dnsd

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

func loadStateMap(path string) map[string]GravityState {
	m := make(map[string]GravityState)
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		json.NewDecoder(f).Decode(&m)
	}
	return m
}

func saveStateMap(path string, m map[string]GravityState) {
	f, err := os.Create(path)
	if err == nil {
		defer f.Close()
		json.NewEncoder(f).Encode(m)
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
	cachePath := filepath.Join(dir, "gravity.cache")
	statePath := filepath.Join(dir, "gravity.state.json")
	
	// Fast path: load from cache if < 24h old (skip for now since we have proper conditional requests)
	if stat, err := os.Stat(cachePath); err == nil {
		if time.Since(stat.ModTime()) < 24*time.Hour {
			return loadCache(cachePath, r)
		}
	}

	stateMap := loadStateMap(statePath)

	tempCache := cachePath + ".tmp"
	f, err := os.Create(tempCache)
	if err != nil {
		return loadCache(cachePath, r) // Fallback to stale cache
	}

	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(tempCache)
		}
	}()

	client := &http.Client{Timeout: 30 * time.Second}
	writer := bufio.NewWriter(f)

	successCount := 0
	notModifiedCount := 0
	
	for _, url := range DefaultLists {
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
			continue
		}
		
		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			notModifiedCount++
			successCount++
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			log.Printf("Failed to fetch %s, status code: %d", url, resp.StatusCode)
			continue
		}
		
		parser := &PiHoleParser{}
		parser.Parse(resp.Body, func(domain string) {
			writer.WriteString(domain + "\n")
		})
		resp.Body.Close()
		
		stateMap[url] = GravityState{
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		}
		successCount++
	}

	writer.Flush()
	
	if successCount == 0 {
		return loadCache(cachePath, r)
	}

	success = true
	f.Close()
	
	if notModifiedCount == len(DefaultLists) {
		// All 304 Not Modified, touch cache and return
		os.Remove(tempCache)
		os.Chtimes(cachePath, time.Now(), time.Now())
		return loadCache(cachePath, r)
	}

	os.Rename(tempCache, cachePath)
	saveStateMap(statePath, stateMap)

	return loadCache(cachePath, r)
}

func loadCache(cachePath string, r *FilterEngine) error {
	f, err := os.Open(cachePath)
	if err != nil {
		return err
	}
	defer f.Close()
	
	scanner := bufio.NewScanner(f)
	newRoot := BuildTrieFromScanner(scanner)
	r.UpdateRoot(newRoot)
	return scanner.Err()
}
