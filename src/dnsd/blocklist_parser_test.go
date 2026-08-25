package dnsd

import (
	"io"
	"runtime"
	"strconv"
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
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
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

func TestParseBlocklist_AdGuardModifiersAndExceptions(t *testing.T) {
	input := `
||ads.example.com^$script,third-party
@@||allow.example.com^$script
||tracker.example.com^$important
example.com##.ad-banner
example.com#@#.ad-banner
example.com#?#div(ad)
example.com#$#body { display:none }
example.com#%#//scriptlet('abort-on-property-read', 'x')
||direct-specific.com
||modifier-only.example.com$important
||url.example.com/path^
||pl.ua^$badfilter
@@||whitelist-only.example.com^
||broken^
`

	expected := []string{
		"ads.example.com",
		"tracker.example.com",
		"direct-specific.com",
		"modifier-only.example.com",
		"url.example.com",
		"broken",
	}

	var result []string
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d domains, got %d (%v)", len(expected), len(result), result)
	}
	for i, domain := range expected {
		if result[i] != domain {
			t.Errorf("expected %s, got %s", domain, result[i])
		}
	}
}

func TestParseBlocklist_BadfilterDisablesMatchingRule(t *testing.T) {
	input := `
||pl.ua^
||pl.ua^$badfilter
`

	var result []string
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected badfilter to suppress matching rule, got %v", result)
	}
}

func TestParseBlocklist_BadfilterDoesNotDisableDifferentRuleVariant(t *testing.T) {
	input := `
||pl.ua^$script
||pl.ua^$important
||pl.ua^$badfilter,script
`

	var result []string
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "pl.ua" {
		t.Fatalf("expected remaining unmatched rule variant to keep pl.ua blocked, got %v", result)
	}
}

func TestParseBlocklist_BadfilterRequiresExactModifierMatch(t *testing.T) {
	input := `
||pl.ua^$badfilterx
`

	var result []string
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "pl.ua" {
		t.Fatalf("expected invalid badfilter lookalike to remain a block rule, got %v", result)
	}
}

func TestParseBlocklist_HostsAliasesAndIPv6(t *testing.T) {
	input := `
127.0.0.1 host.example alias.example
::1 ipv6.example ipv6-alias.example
`

	expected := []string{
		"host.example",
		"alias.example",
		"ipv6.example",
		"ipv6-alias.example",
	}

	var result []string
	parser := &BlocklistParser{}
	err := parser.Parse(strings.NewReader(input), func(domain string) {
		result = append(result, domain)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(expected) {
		t.Fatalf("expected %d domains, got %d (%v)", len(expected), len(result), result)
	}
	for i, domain := range expected {
		if result[i] != domain {
			t.Errorf("expected %s, got %s", domain, result[i])
		}
	}
}

type repeatedRuleReader struct {
	rule      string
	remaining int
	buffer    []byte
}

func (r *repeatedRuleReader) Read(p []byte) (int, error) {
	for len(r.buffer) == 0 && r.remaining > 0 {
		r.buffer = []byte(r.rule)
		r.remaining--
	}
	if len(r.buffer) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}

type uniqueRuleReader struct {
	current   int
	total     int
	pending   []byte
	linePrefix string
	lineSuffix string
}

func (r *uniqueRuleReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 && r.current < r.total {
		r.pending = []byte(r.linePrefix + strconv.Itoa(r.current) + r.lineSuffix)
		r.current++
	}
	if len(r.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func TestParseBlocklist_BoundedMemoryOnLargeRepeatedRules(t *testing.T) {
	parser := &BlocklistParser{}
	reader := &repeatedRuleReader{
		rule:      "||repeat.example.com^$script\n",
		remaining: 250000,
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	emittedCount := 0
	err := parser.Parse(reader, func(domain string) {
		emittedCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emittedCount != 1 {
		t.Fatalf("expected duplicate rules to coalesce to one emitted domain, got %d", emittedCount)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if delta := int64(after.Alloc) - int64(before.Alloc); delta > 8<<20 {
		t.Fatalf("expected bounded heap growth for repeated rules, got %d bytes", delta)
	}
}

func TestParseBlocklist_BoundedMemoryOnLargeUniqueRules(t *testing.T) {
	parser := &BlocklistParser{}
	reader := &uniqueRuleReader{
		total:      600000,
		linePrefix: "||rule-",
		lineSuffix: ".example.com^$script\n",
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	maxAlloc := before.Alloc

	originalHook := blocklistParserProgressHook
	blocklistParserProgressHook = func(int) {
		var snapshot runtime.MemStats
		runtime.ReadMemStats(&snapshot)
		if snapshot.Alloc > maxAlloc {
			maxAlloc = snapshot.Alloc
		}
	}
	defer func() { blocklistParserProgressHook = originalHook }()

	emittedCount := 0
	err := parser.Parse(reader, func(domain string) {
		emittedCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emittedCount != 600000 {
		t.Fatalf("expected all unique rules to emit, got %d", emittedCount)
	}
	t.Logf("peak alloc delta bytes: %d", int64(maxAlloc)-int64(before.Alloc))
	if delta := int64(maxAlloc) - int64(before.Alloc); delta > 24<<20 {
		t.Fatalf("expected bounded peak heap growth for unique rules, got %d bytes", delta)
	}
}
