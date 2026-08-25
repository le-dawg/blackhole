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

	p1 := CacheQueryParams{
		QName:   "example.com.",
		QType:   uint16(dnsmessage.TypeA),
		QClass:  uint16(dnsmessage.ClassINET),
		RD:      true,
		CD:      false,
		AD:      false,
		HasDO:   false,
		EDNSUDP: 1232,
	}

	cache.Set(p1, msg)

	cachedMsg, hit := cache.Get(p1)
	if !hit {
		t.Fatal("expected cache hit")
	}
	if len(cachedMsg.Answers) != 1 {
		t.Fatal("expected 1 answer")
	}

	// CD flag mismatch should miss
	pCD := p1
	pCD.CD = true
	if _, hit = cache.Get(pCD); hit {
		t.Fatal("expected cache miss for different CD flag")
	}

	// DO flag mismatch should miss
	pDO := p1
	pDO.HasDO = true
	if _, hit = cache.Get(pDO); hit {
		t.Fatal("expected cache miss for different DO flag")
	}

	// EDNS payload size mismatch should miss
	pEDNS := p1
	pEDNS.EDNSUDP = 4096
	if _, hit = cache.Get(pEDNS); hit {
		t.Fatal("expected cache miss for different EDNS UDP payload size")
	}

	// Test eviction
	p2 := p1
	p2.QName = "example2.com."
	p3 := p1
	p3.QName = "example3.com."
	cache.Set(p2, msg)
	cache.Set(p3, msg) // Should evict example.com

	if _, hit = cache.Get(p1); hit {
		t.Fatal("expected example.com. to be evicted")
	}
}

func TestDNSCache_ZeroTTLNotCached(t *testing.T) {
	cache := NewDNSCache(2)
	msg := &dnsmessage.Message{
		Header: dnsmessage.Header{
			Response: true,
		},
		Answers: []dnsmessage.Resource{
			{
				Header: dnsmessage.ResourceHeader{
					Name:  dnsmessage.MustNewName("zero.example.com."),
					Type:  dnsmessage.TypeA,
					Class: dnsmessage.ClassINET,
					TTL:   0,
				},
				Body: &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}},
			},
		},
	}

	p := CacheQueryParams{
		QName:  "zero.example.com.",
		QType:  uint16(dnsmessage.TypeA),
		QClass: uint16(dnsmessage.ClassINET),
	}

	cache.Set(p, msg)
	if _, hit := cache.Get(p); hit {
		t.Fatal("expected 0-TTL response NOT to be cached")
	}
}
