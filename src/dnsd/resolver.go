package dnsd

import (
	"strings"
	"sync"
)

type TrieNode struct {
	children map[string]*TrieNode
	isEnd    bool
}

type Resolver struct {
	mu        sync.RWMutex
	root      *TrieNode
	upstreams []string
}

func NewResolver(upstreams []string) *Resolver {
	return &Resolver{
		root:      &TrieNode{},
		upstreams: upstreams,
	}
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")
	return domain
}

func (r *Resolver) initRootIfNeeded() {
	r.mu.RLock()
	if r.root != nil {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.root == nil {
		r.root = &TrieNode{}
	}
}

func (r *Resolver) AddBlockedDomain(domain string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}

	r.initRootIfNeeded()

	parts := strings.Split(domain, ".")

	r.mu.Lock()
	defer r.mu.Unlock()

	node := r.root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		if node.isEnd {
			// A parent domain is already blocked, so this subdomain is implicitly blocked.
			// No need to insert further.
			return
		}
		if node.children == nil {
			node.children = make(map[string]*TrieNode)
		}
		if _, exists := node.children[part]; !exists {
			node.children[part] = &TrieNode{}
		}
		node = node.children[part]
	}
	node.isEnd = true
	// Since this node is now blocked, all its children (more specific subdomains) are redundant.
	// We can clear its children map to save memory.
	node.children = nil
}

func (r *Resolver) Resolve(domain string) bool {
	domain = normalizeDomain(domain)
	if domain == "" {
		return false
	}

	r.initRootIfNeeded()

	r.mu.RLock()
	defer r.mu.RUnlock()

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
