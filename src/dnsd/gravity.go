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
			resp.Body.Close()
			continue
		}

		writer := bufio.NewWriter(f)
		parser := &PiHoleParser{}
		parser.Parse(resp.Body, func(domain string) {
			writer.WriteString(domain + "\n")
		})
		writer.Flush()
		f.Close()
		resp.Body.Close()

		os.Rename(tempCache, cachePath)

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
