package dnsd

import (
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestDNSCache(t *testing.T) {
	cache := NewDNSCache(2)

	msg := &dnsmessage.Message{
		Header: dnsmessage.Header{
			Response: true,
		},
		Answers: []dnsmessage.Resource{
			{
				Header: dnsmessage.ResourceHeader{
					Name:  dnsmessage.MustNewName("example.com."),
					Type:  dnsmessage.TypeA,
					Class: dnsmessage.ClassINET,
					TTL:   100,
				},
				Body: &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}},
			},
		},
	}

	cache.Set("example.com.", uint16(dnsmessage.TypeA), uint16(dnsmessage.ClassINET), msg)

	cachedMsg, hit := cache.Get("example.com.", uint16(dnsmessage.TypeA), uint16(dnsmessage.ClassINET))
	if !hit {
		t.Fatal("expected cache hit")
	}
	if len(cachedMsg.Answers) != 1 {
		t.Fatal("expected 1 answer")
	}

	// Test eviction
	cache.Set("example2.com.", uint16(dnsmessage.TypeA), uint16(dnsmessage.ClassINET), msg)
	cache.Set("example3.com.", uint16(dnsmessage.TypeA), uint16(dnsmessage.ClassINET), msg) // Should evict example.com

	_, hit = cache.Get("example.com.", uint16(dnsmessage.TypeA), uint16(dnsmessage.ClassINET))
	if hit {
		t.Fatal("expected example.com. to be evicted")
	}
}
