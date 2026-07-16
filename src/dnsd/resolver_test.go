package dnsd

import (
	"fmt"
	"testing"
)

func TestTrieResolver(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads.doubleclick.net")
	r.AddBlockedDomain("adservice.google.com")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to be blocked")
	}
	if !r.Resolve("sub.ads.doubleclick.net") {
		t.Error("Expected subdomains of blocked domains to be blocked")
	}
	if r.Resolve("google.com") {
		t.Error("Expected google.com to NOT be blocked")
	}
}

func TestResolverCaseInsensitivity(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("Ads.DoubleClick.Net")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected lowercase lookup to match mixed-case addition")
	}
	if !r.Resolve("ADS.DOUBLECLICK.NET") {
		t.Error("Expected uppercase lookup to match mixed-case addition")
	}
	if !r.Resolve("Sub.Ads.DoubleClick.Net") {
		t.Error("Expected subdomain lookup with mixed case to match")
	}
}

func TestResolverTrailingDot(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads.doubleclick.net.")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected lookup without trailing dot to match addition with trailing dot")
	}
	if !r.Resolve("ads.doubleclick.net.") {
		t.Error("Expected lookup with trailing dot to match addition with trailing dot")
	}

	r.AddBlockedDomain("adservice.google.com")
	if !r.Resolve("adservice.google.com.") {
		t.Error("Expected lookup with trailing dot to match addition without trailing dot")
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads.doubleclick.net")

	done := make(chan bool)
	const numGoroutines = 50
	const numOps = 100

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numOps; j++ {
				r.Resolve("ads.doubleclick.net")
				r.Resolve("sub.ads.doubleclick.net")
				r.Resolve("google.com")
			}
			done <- true
		}()
	}

	// Writers
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 20; j++ {
				domain := fmt.Sprintf("blocked-%d-%d.com", id, j)
				r.AddBlockedDomain(domain)
			}
			done <- true
		}(i)
	}

	for i := 0; i < numGoroutines+5; i++ {
		<-done
	}
}
