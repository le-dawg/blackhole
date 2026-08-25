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
	if max <= 0 {
		max = 1000
	}
	return &DNSCache{
		entries:    make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: max,
	}
}

func cacheKey(qname string, qtype, qclass uint16) string {
	return fmt.Sprintf("%s|%d|%d", qname, qtype, qclass)
}

func cloneResource(r dnsmessage.Resource) dnsmessage.Resource {
	resCopy := r
	switch b := r.Body.(type) {
	case *dnsmessage.AResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.AAAAResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.CNAMEResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.TXTResource:
		body := *b
		body.TXT = make([]string, len(b.TXT))
		copy(body.TXT, b.TXT)
		resCopy.Body = &body
	case *dnsmessage.PTRResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.MXResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.NSResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.SOAResource:
		body := *b
		resCopy.Body = &body
	case *dnsmessage.OPTResource:
		body := *b
		body.Options = make([]dnsmessage.Option, len(b.Options))
		for i, opt := range b.Options {
			optCopy := opt
			optCopy.Data = make([]byte, len(opt.Data))
			copy(optCopy.Data, opt.Data)
			body.Options[i] = optCopy
		}
		resCopy.Body = &body
	}
	return resCopy
}

func cloneMessage(msg *dnsmessage.Message) *dnsmessage.Message {
	if msg == nil {
		return nil
	}
	msgCopy := *msg

	msgCopy.Questions = make([]dnsmessage.Question, len(msg.Questions))
	copy(msgCopy.Questions, msg.Questions)

	msgCopy.Answers = make([]dnsmessage.Resource, len(msg.Answers))
	for i, a := range msg.Answers {
		msgCopy.Answers[i] = cloneResource(a)
	}

	msgCopy.Authorities = make([]dnsmessage.Resource, len(msg.Authorities))
	for i, a := range msg.Authorities {
		msgCopy.Authorities[i] = cloneResource(a)
	}

	msgCopy.Additionals = make([]dnsmessage.Resource, len(msg.Additionals))
	for i, a := range msg.Additionals {
		msgCopy.Additionals[i] = cloneResource(a)
	}

	return &msgCopy
}

func (c *DNSCache) Get(qname string, qtype, qclass uint16) (*dnsmessage.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(qname, qtype, qclass)
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
		if msgCopy.Answers[i].Header.Type != dnsmessage.TypeOPT {
			msgCopy.Answers[i].Header.TTL = ttlRemaining
		}
	}
	for i := range msgCopy.Authorities {
		if msgCopy.Authorities[i].Header.Type != dnsmessage.TypeOPT {
			msgCopy.Authorities[i].Header.TTL = ttlRemaining
		}
	}
	for i := range msgCopy.Additionals {
		// RFC 6891: OPT TTL encodes extended RCODE and flags, never overwrite
		if msgCopy.Additionals[i].Header.Type != dnsmessage.TypeOPT {
			msgCopy.Additionals[i].Header.TTL = ttlRemaining
		}
	}

	return msgCopy, true
}

func (c *DNSCache) Set(qname string, qtype, qclass uint16, msg *dnsmessage.Message) {
	if len(msg.Answers) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(qname, qtype, qclass)

	var minTTL uint32 = 0
	for _, ans := range msg.Answers {
		if ans.Header.Type != dnsmessage.TypeOPT {
			if minTTL == 0 || ans.Header.TTL < minTTL {
				minTTL = ans.Header.TTL
			}
		}
	}
	for _, auth := range msg.Authorities {
		if auth.Header.Type != dnsmessage.TypeOPT {
			if minTTL == 0 || auth.Header.TTL < minTTL {
				minTTL = auth.Header.TTL
			}
		}
	}
	if minTTL == 0 {
		minTTL = 60
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
