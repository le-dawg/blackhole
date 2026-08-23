package dnsd

import (
	"bufio"
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
	
	// Fast path: load from cache if < 24h old
	if stat, err := os.Stat(cachePath); err == nil {
		if time.Since(stat.ModTime()) < 24*time.Hour {
			return loadCache(cachePath, r)
		}
	}

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
	for _, url := range DefaultLists {
		resp, err := client.Get(url)
		if err != nil || resp.StatusCode != 200 {
			log.Printf("Failed to fetch %s: %v", url, err)
			continue
		}
		
		ParseBlocklist(resp.Body, func(domain string) {
			writer.WriteString(domain + "\n")
		})
		resp.Body.Close()
		successCount++
	}

	writer.Flush()
	if successCount == 0 {
		return loadCache(cachePath, r)
	}

	success = true
	f.Close()
	os.Rename(tempCache, cachePath)

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
