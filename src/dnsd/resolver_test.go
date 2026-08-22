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

func TestZeroValueResolver(t *testing.T) {
	var r Resolver
	// Test AddBlockedDomain works on zero value
	r.AddBlockedDomain("ads.doubleclick.net")

	// Test Resolve works on zero value
	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to be blocked on zero-value resolver")
	}
	if r.Resolve("google.com") {
		t.Error("Expected google.com to not be blocked")
	}
}

func TestWhitespaceNormalization(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("  ads.doubleclick.net  ")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to be blocked")
	}
	if !r.Resolve("  ads.doubleclick.net  ") {
		t.Error("Expected padded lookup to be blocked")
	}
}

func TestConsecutiveDots(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads..doubleclick.net")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected normalized domain to match domain with consecutive dots")
	}
	if !r.Resolve("ads..doubleclick.net") {
		t.Error("Expected consecutive dot lookup to match")
	}
}

func TestMalformedInputsDoNotCorruptResolver(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})

	// Verify that adding valid domains works.
	r.AddBlockedDomain("ads.doubleclick.net")
	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to be blocked initially")
	}

	// Try adding malformed domains that could potentially corrupt the root node.
	malformed := []string{"", "..", "...", "  .  ", "  ", "."}
	for _, m := range malformed {
		r.AddBlockedDomain(m)
	}

	// Verify that the root trie node is not corrupted (meaning a normal domain is NOT blocked).
	if r.Resolve("google.com") {
		t.Error("Expected google.com to NOT be blocked after adding malformed inputs")
	}
	if r.Resolve("yahoo.com") {
		t.Error("Expected yahoo.com to NOT be blocked after adding malformed inputs")
	}

	// Verify that the valid domains are still blocked.
	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to remain blocked after adding malformed inputs")
	}
	if !r.Resolve("sub.ads.doubleclick.net") {
		t.Error("Expected sub.ads.doubleclick.net to remain blocked after adding malformed inputs")
	}
}

func TestResolverMultipleTrailingDots(t *testing.T) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads.doubleclick.net...")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected lookup without trailing dots to match addition with multiple trailing dots")
	}
	if !r.Resolve("ads.doubleclick.net.") {
		t.Error("Expected lookup with single trailing dot to match addition with multiple trailing dots")
	}
	if !r.Resolve("ads.doubleclick.net...") {
		t.Error("Expected lookup with multiple trailing dots to match addition with multiple trailing dots")
	}

	r.AddBlockedDomain("adservice.google.com")
	if !r.Resolve("adservice.google.com...") {
		t.Error("Expected lookup with multiple trailing dots to match addition without trailing dots")
	}
}

func BenchmarkResolve(b *testing.B) {
	r := NewResolver([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads.doubleclick.net")
	r.AddBlockedDomain("adservice.google.com")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Resolve("ads.doubleclick.net")
		r.Resolve("google.com")
	}
}

func TestCanaryDomains(t *testing.T) {
	r := NewResolver(nil)
	if !r.Resolve("use-application-dns.net.") {
		t.Error("Firefox canary should be blocked")
	}
	if !r.Resolve("dns.google.") {
		t.Error("Chrome canary should be blocked")
	}
}

