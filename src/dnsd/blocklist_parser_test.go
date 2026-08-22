package dnsd

import (
	"strings"
	"testing"
)

func TestParseBlocklist(t *testing.T) {
	input := `
# A comment
! Another comment
0.0.0.0 hosts.example.com
127.0.0.1 loopback.example.com
plain.example.com
||adguard.example.com^
  spaces.example.com  
192.168.1.1 ignored.example.com
inline.example.com # inline comment
inline2.example.com ! inline comment 2
||^
`
	expected := []string{
		"hosts.example.com",
		"loopback.example.com",
		"plain.example.com",
		"adguard.example.com",
		"spaces.example.com",
		"inline.example.com",
		"inline2.example.com",
	}

	var result []string
	err := ParseBlocklist(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d domains, got %d", len(expected), len(result))
	}
	for i, domain := range expected {
		if result[i] != domain {
			t.Errorf("expected %s, got %s", domain, result[i])
		}
	}
}
