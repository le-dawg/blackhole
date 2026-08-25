package dnsd

import (
	"bufio"
	"strings"
	"sync"
	"testing"
)

func TestTrieFilterEngine(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
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

func TestFilterEngineCaseInsensitivity(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
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

func TestFilterEngineTrailingDot(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
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





func TestWhitespaceNormalization(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
	r.AddBlockedDomain("  ads.doubleclick.net  ")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected ads.doubleclick.net to be blocked")
	}
	if !r.Resolve("  ads.doubleclick.net  ") {
		t.Error("Expected padded lookup to be blocked")
	}
}

func TestConsecutiveDots(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
	r.AddBlockedDomain("ads..doubleclick.net")

	if !r.Resolve("ads.doubleclick.net") {
		t.Error("Expected normalized domain to match domain with consecutive dots")
	}
	if !r.Resolve("ads..doubleclick.net") {
		t.Error("Expected consecutive dot lookup to match")
	}
}

func TestMalformedInputsDoNotCorruptFilterEngine(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})

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

func TestFilterEngineMultipleTrailingDots(t *testing.T) {
	r := NewFilterEngine([]string{"1.1.1.1"})
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
	r := NewFilterEngine([]string{"1.1.1.1"})
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
	r := NewFilterEngine(nil)
	if !r.Resolve("use-application-dns.net.") {
		t.Error("Firefox canary should be blocked")
	}
	if !r.Resolve("dns.google.") {
		t.Error("Chrome canary should be blocked")
	}
}

func TestFilterEngine_ConcurrentRootAndListUpdatesPreserveBoth(t *testing.T) {
	for i := 0; i < 5000; i++ {
		r := NewFilterEngine(nil)
		root := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("blocked.example.com\n")))

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			r.UpdateRoot(root)
		}()

		go func() {
			defer wg.Done()
			<-start
			r.SetLists(
				map[string]bool{"allowed.example.com": true},
				map[string]bool{"forced.example.com": true},
			)
		}()

		close(start)
		wg.Wait()

		if !r.Resolve("blocked.example.com") {
			t.Fatalf("iteration %d: lost trie update", i)
		}
		if r.Resolve("allowed.example.com") {
			t.Fatalf("iteration %d: lost whitelist update", i)
		}
		if !r.Resolve("forced.example.com") {
			t.Fatalf("iteration %d: lost blacklist update", i)
		}
	}
}

func TestFilterEngine_SetListsClonesPublishedMaps(t *testing.T) {
	r := NewFilterEngine(nil)
	whitelist := map[string]bool{"allowed.example.com": true}
	blacklist := map[string]bool{"blocked.example.com": true}

	r.SetLists(whitelist, blacklist)

	whitelist["later-allowed.example.com"] = true
	delete(blacklist, "blocked.example.com")
	blacklist["later-blocked.example.com"] = true

	if !r.Resolve("blocked.example.com") {
		t.Fatal("expected originally published blacklist entry to remain active")
	}
	if r.Resolve("later-allowed.example.com") {
		t.Fatal("did not expect post-publication whitelist mutation to affect resolver state")
	}
	if r.Resolve("later-blocked.example.com") {
		t.Fatal("did not expect post-publication blacklist mutation to affect resolver state")
	}
}

func TestFilterEngine_GravityAllowlistOverridesParentBlock(t *testing.T) {
	r := NewFilterEngine(nil)
	root := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("example.com\n")))

	r.UpdateGravityData(root, map[string]bool{"ads.example.com": true})

	if r.Resolve("example.com") != true {
		t.Fatal("expected parent domain to remain blocked")
	}
	if r.Resolve("ads.example.com") {
		t.Fatal("expected gravity allowlist to override inherited parent-domain block")
	}
	if r.Resolve("child.ads.example.com") {
		t.Fatal("expected gravity allowlist to apply to subdomains of the allowed domain")
	}
	if !r.Resolve("other.example.com") {
		t.Fatal("expected unrelated sibling under blocked parent to remain blocked")
	}
}

func TestFilterEngine_BlacklistOverridesWhitelistAndGravityAllowlist(t *testing.T) {
	r := NewFilterEngine(nil)
	root := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("example.com\n")))

	r.UpdateGravityData(root, map[string]bool{"ads.example.com": true})
	r.SetLists(
		map[string]bool{"ads.example.com": true},
		map[string]bool{"ads.example.com": true},
	)

	if !r.Resolve("ads.example.com") {
		t.Fatal("expected explicit blacklist to override whitelist and gravity allowlist")
	}
}
