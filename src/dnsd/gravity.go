package dnsd

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var DefaultLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

func StartGravitySync(dir string, r *Resolver) {
	go func() {
		refreshGravity(dir, r)
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			refreshGravity(dir, r)
		}
	}()
}

func refreshGravity(dir string, r *Resolver) error {
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

	client := &http.Client{Timeout: 30 * time.Second}
	writer := bufio.NewWriter(f)

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
	}

	writer.Flush()
	f.Close()
	os.Rename(tempCache, cachePath)

	return loadCache(cachePath, r)
}

func loadCache(cachePath string, r *Resolver) error {
	f, err := os.Open(cachePath)
	if err != nil {
		return err
	}
	defer f.Close()

	newRoot := &trieNode{}
	
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			addDomainToRoot(newRoot, line)
		}
	}

	r.mu.Lock()
	r.root = newRoot
	r.mu.Unlock()
	return scanner.Err()
}

func addDomainToRoot(root *trieNode, domain string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}

	parts := strings.Split(domain, ".")

	node := root
	inserted := false
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		inserted = true
		if node.isEnd {
			return
		}
		if node.children == nil {
			node.children = make(map[string]*trieNode)
		}
		if _, exists := node.children[part]; !exists {
			node.children[part] = &trieNode{}
		}
		node = node.children[part]
	}
	if !inserted {
		return
	}
	node.isEnd = true
	node.children = nil
}
