package dnsd

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type cacheEntry struct {
	key       string
	msg       *dnsmessage.Message
	expiresAt time.Time
}

type DNSCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lruList    *list.List
	maxEntries int
}

func NewDNSCache(max int) *DNSCache {
	return &DNSCache{
		entries:    make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: max,
	}
}

func cacheKey(qname string, qtype uint16) string {
	return fmt.Sprintf("%s|%d", qname, qtype)
}

func cloneMessage(msg *dnsmessage.Message) *dnsmessage.Message {
	if msg == nil {
		return nil
	}
	msgCopy := *msg

	msgCopy.Questions = make([]dnsmessage.Question, len(msg.Questions))
	copy(msgCopy.Questions, msg.Questions)

	msgCopy.Answers = make([]dnsmessage.Resource, len(msg.Answers))
	copy(msgCopy.Answers, msg.Answers)

	msgCopy.Authorities = make([]dnsmessage.Resource, len(msg.Authorities))
	copy(msgCopy.Authorities, msg.Authorities)

	msgCopy.Additionals = make([]dnsmessage.Resource, len(msg.Additionals))
	copy(msgCopy.Additionals, msg.Additionals)

	return &msgCopy
}

func (c *DNSCache) Get(qname string, qtype uint16) (*dnsmessage.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(qname, qtype)
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)
	now := time.Now()

	if now.After(entry.expiresAt) {
		c.lruList.Remove(elem)
		delete(c.entries, key)
		return nil, false
	}

	c.lruList.MoveToFront(elem)

	msgCopy := cloneMessage(entry.msg)

	ttlRemaining := uint32(entry.expiresAt.Sub(now).Seconds())
	for i := range msgCopy.Answers {
		msgCopy.Answers[i].Header.TTL = ttlRemaining
	}

	return msgCopy, true
}

func (c *DNSCache) Set(qname string, qtype uint16, msg *dnsmessage.Message) {
	if len(msg.Answers) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(qname, qtype)

	var minTTL uint32 = msg.Answers[0].Header.TTL
	for _, ans := range msg.Answers {
		if ans.Header.TTL < minTTL {
			minTTL = ans.Header.TTL
		}
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(minTTL) * time.Second)
	msgCopy := cloneMessage(msg)

	if elem, exists := c.entries[key]; exists {
		entry := elem.Value.(*cacheEntry)
		entry.msg = msgCopy
		entry.expiresAt = expiresAt
		c.lruList.MoveToFront(elem)
		return
	}

	if len(c.entries) >= c.maxEntries {
		elem := c.lruList.Back()
		if elem != nil {
			oldEntry := elem.Value.(*cacheEntry)
			delete(c.entries, oldEntry.key)
			c.lruList.Remove(elem)
		}
	}

	entry := &cacheEntry{
		key:       key,
		msg:       msgCopy,
		expiresAt: expiresAt,
	}
	elem := c.lruList.PushFront(entry)
	c.entries[key] = elem
}

func (c *DNSCache) Flush() {
	c.mu.Lock()
	c.entries = make(map[string]*list.Element)
	c.lruList.Init()
	c.mu.Unlock()
}
