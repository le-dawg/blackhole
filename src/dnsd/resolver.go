package dnsd

import (
	"strings"
	"sync"
)

type trieNode struct {
	children map[string]*trieNode
	isEnd    bool
}

// Resolver is a concurrency-safe DNS blocklist resolver that uses a trie structure
// to match domain queries against a list of blocked domains. It supports efficient
// lookup of subdomains under blocked parent domains.
type Resolver struct {
	mu        sync.RWMutex
	root      *trieNode
	upstreams []string
}

// NewResolver initializes and returns a new *Resolver with the provided upstream DNS servers.
func NewResolver(upstreams []string) *Resolver {
	return &Resolver{
		root:      &trieNode{},
		upstreams: upstreams,
	}
}

func needsNormalization(domain string) bool {
	if domain == "" {
		return false
	}
	// Check first character for whitespace
	first := domain[0]
	if first <= ' ' {
		return true
	}
	// Check last character for whitespace or dot
	last := domain[len(domain)-1]
	if last <= ' ' || last == '.' {
		return true
	}
	// Check for any uppercase character, or non-ASCII spaces/chars
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if c >= 'A' && c <= 'Z' {
			return true
		}
		if c > 127 {
			return true
		}
	}
	return false
}

func normalizeDomain(domain string) string {
	if !needsNormalization(domain) {
		return domain
	}
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain)
	domain = strings.TrimRight(domain, ".")
	return domain
}

// AddBlockedDomain normalizes and inserts a domain into the resolver's blocked trie.
// It is safe for concurrent use. If a parent domain is already blocked, any subdomain
// insertion is optimized away.
func (r *Resolver) AddBlockedDomain(domain string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}

	parts := strings.Split(domain, ".")

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.root == nil {
		r.root = &trieNode{}
	}

	node := r.root
	inserted := false
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		inserted = true
		if node.isEnd {
			// A parent domain is already blocked, so this subdomain is implicitly blocked.
			// No need to insert further.
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
	// Since this node is now blocked, all its children (more specific subdomains) are redundant.
	// We can clear its children map to save memory.
	node.children = nil
}

// Resolve normalizes a domain and queries the trie to check if it is blocked.
// It returns true if the domain or any of its parent domains are blocked, and false otherwise.
// It is safe for concurrent use.
func (r *Resolver) Resolve(domain string) bool {
	domain = normalizeDomain(domain)
	if domain == "" {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.root == nil {
		return false
	}

	node := r.root
	end := len(domain)
	for end > 0 {
		start := end - 1
		for start >= 0 && domain[start] != '.' {
			start--
		}
		part := domain[start+1 : end]
		end = start

		if part == "" {
			continue
		}

		if node.children == nil {
			return false
		}
		nextNode, exists := node.children[part]
		if !exists {
			return false
		}
		node = nextNode
		if node.isEnd {
			return true
		}
	}
	return false
}
