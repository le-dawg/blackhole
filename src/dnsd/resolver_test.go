package dnsd

import "testing"

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
