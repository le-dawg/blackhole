package dnsd

import (
	"bufio"
	"strings"
	"sync/atomic"
)

type trieNode struct {
	children map[string]*trieNode
	isEnd    bool
}

type engineState struct {
	root      *trieNode
	whitelist map[string]bool
	blacklist map[string]bool
}

// FilterEngine is a concurrency-safe DNS blocklist resolver backed by atomic.Value.
type FilterEngine struct {
	state     atomic.Value
	upstreams []string
}

// NewFilterEngine initializes and returns a new *FilterEngine.
func NewFilterEngine(upstreams []string) *FilterEngine {
	e := &FilterEngine{
		upstreams: upstreams,
	}
	e.state.Store(&engineState{
		root:      &trieNode{},
		whitelist: make(map[string]bool),
		blacklist: make(map[string]bool),
	})
	return e
}

func needsNormalization(domain string) bool {
	if domain == "" {
		return false
	}
	first := domain[0]
	if first <= ' ' {
		return true
	}
	last := domain[len(domain)-1]
	if last <= ' ' || last == '.' {
		return true
	}
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

// BuildTrieFromScanner reads domains from a scanner and builds a new Radix tree.
// It returns the new root node.
func BuildTrieFromScanner(scanner *bufio.Scanner) *trieNode {
	newRoot := &trieNode{}
	
	for scanner.Scan() {
		domain := normalizeDomain(scanner.Text())
		if domain == "" {
			continue
		}

		parts := strings.Split(domain, ".")

		node := newRoot
		inserted := false
		for i := len(parts) - 1; i >= 0; i-- {
			part := parts[i]
			if part == "" {
				continue
			}
			inserted = true
			if node.isEnd {
				break
			}
			if node.children == nil {
				node.children = make(map[string]*trieNode)
			}
			if _, exists := node.children[part]; !exists {
				node.children[part] = &trieNode{}
			}
			node = node.children[part]
		}
		if inserted {
			node.isEnd = true
			node.children = nil
		}
	}
	return newRoot
}

// UpdateRoot completely replaces the radix tree in a zero-downtime pointer swap.
func (e *FilterEngine) UpdateRoot(newRoot *trieNode) {
	oldState := e.state.Load().(*engineState)
	newState := &engineState{
		root:      newRoot,
		whitelist: oldState.whitelist,
		blacklist: oldState.blacklist,
	}
	e.state.Store(newState)
}

// SetLists updates the whitelist and blacklist.
func (e *FilterEngine) SetLists(whitelist, blacklist map[string]bool) {
	oldState := e.state.Load().(*engineState)
	newState := &engineState{
		root:      oldState.root,
		whitelist: whitelist,
		blacklist: blacklist,
	}
	e.state.Store(newState)
}

// Resolve checks if a domain is blocked.
func (e *FilterEngine) Resolve(domain string) bool {
	domain = normalizeDomain(domain)
	if domain == "" {
		return false
	}

	switch domain {
	case "use-application-dns.net", "dns.google", "cloudflare-dns.com", "doh.opendns.com":
		return true
	}

	state := e.state.Load().(*engineState)

	if state.whitelist != nil && state.whitelist[domain] {
		return false
	}
	if state.blacklist != nil && state.blacklist[domain] {
		return true
	}

	if state.root == nil {
		return false
	}

	node := state.root
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

// AddBlockedDomain is provided for test compatibility.
// It mutates the active tree in place without concurrency safety.
func (e *FilterEngine) AddBlockedDomain(domain string) {
	state := e.state.Load().(*engineState)
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}
	parts := strings.Split(domain, ".")
	node := state.root
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
	if inserted {
		node.isEnd = true
		node.children = nil
	}
}
