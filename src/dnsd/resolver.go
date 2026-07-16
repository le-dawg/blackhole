package dnsd

import "strings"

type TrieNode struct {
	children map[string]*TrieNode
	isEnd    bool
}

type Resolver struct {
	root      *TrieNode
	upstreams []string
}

func NewResolver(upstreams []string) *Resolver {
	return &Resolver{
		root:      &TrieNode{children: make(map[string]*TrieNode)},
		upstreams: upstreams,
	}
}

func (r *Resolver) AddBlockedDomain(domain string) {
	parts := strings.Split(domain, ".")
	node := r.root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if _, exists := node.children[part]; !exists {
			node.children[part] = &TrieNode{children: make(map[string]*TrieNode)}
		}
		node = node.children[part]
	}
	node.isEnd = true
}

func (r *Resolver) Resolve(domain string) bool {
	parts := strings.Split(domain, ".")
	node := r.root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if nextNode, exists := node.children[part]; exists {
			node = nextNode
			if node.isEnd {
				return true
			}
		} else {
			break
		}
	}
	return false
}
