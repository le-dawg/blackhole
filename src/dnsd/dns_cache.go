package dnsd

import (
	"sync"
	"time"
	"golang.org/x/net/dns/dnsmessage"
)

type cacheEntry struct {
	msg       *dnsmessage.Message
	expiresAt time.Time
	lastUsed  time.Time
}

type DNSCache struct {
	mu         sync.RWMutex
	entries    map[string]*cacheEntry
	maxEntries int
}

func NewDNSCache(max int) *DNSCache {
	return &DNSCache{
		entries:    make(map[string]*cacheEntry),
		maxEntries: max,
	}
}

func cacheKey(qname string, qtype uint16) string {
	return qname + "|" + string(rune(qtype))
}

func (c *DNSCache) Get(qname string, qtype uint16) (*dnsmessage.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	key := cacheKey(qname, qtype)
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	
	entry.lastUsed = time.Now()
	
	// Deep copy msg and decrement TTL
	msgCopy := *entry.msg // shallow copy the Message struct
	
	// deep copy arrays/slices inside Message
	msgCopy.Questions = make([]dnsmessage.Question, len(entry.msg.Questions))
	copy(msgCopy.Questions, entry.msg.Questions)
	
	msgCopy.Answers = make([]dnsmessage.Resource, len(entry.msg.Answers))
	copy(msgCopy.Answers, entry.msg.Answers)
	
	msgCopy.Authorities = make([]dnsmessage.Resource, len(entry.msg.Authorities))
	copy(msgCopy.Authorities, entry.msg.Authorities)
	
	msgCopy.Additionals = make([]dnsmessage.Resource, len(entry.msg.Additionals))
	copy(msgCopy.Additionals, entry.msg.Additionals)
	
	ttlRemaining := uint32(time.Until(entry.expiresAt).Seconds())
	for i := range msgCopy.Answers {
		msgCopy.Answers[i].Header.TTL = ttlRemaining
	}
	
	return &msgCopy, true
}

func (c *DNSCache) Set(qname string, qtype uint16, msg *dnsmessage.Message) {
	if len(msg.Answers) == 0 {
		return
	}
	
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if len(c.entries) >= c.maxEntries {
		c.evictOldest()
	}
	
	ttl := msg.Answers[0].Header.TTL
	
	// Deep copy before storing
	msgCopy := *msg
	
	msgCopy.Questions = make([]dnsmessage.Question, len(msg.Questions))
	copy(msgCopy.Questions, msg.Questions)
	
	msgCopy.Answers = make([]dnsmessage.Resource, len(msg.Answers))
	copy(msgCopy.Answers, msg.Answers)
	
	msgCopy.Authorities = make([]dnsmessage.Resource, len(msg.Authorities))
	copy(msgCopy.Authorities, msg.Authorities)
	
	msgCopy.Additionals = make([]dnsmessage.Resource, len(msg.Additionals))
	copy(msgCopy.Additionals, msg.Additionals)
	
	key := cacheKey(qname, qtype)
	c.entries[key] = &cacheEntry{
		msg:       &msgCopy,
		expiresAt: time.Now().Add(time.Duration(ttl) * time.Second),
		lastUsed:  time.Now(),
	}
}

func (c *DNSCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	
	first := true
	for k, v := range c.entries {
		if first || v.lastUsed.Before(oldestTime) {
			oldestTime = v.lastUsed
			oldestKey = k
			first = false
		}
	}
	if !first {
		delete(c.entries, oldestKey)
	}
}

func (c *DNSCache) Flush() {
	c.mu.Lock()
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()
}
