package dnsd

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGravitySync(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 ads.test.com\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL} // Override for test

	res := NewFilterEngine(nil)

	err := refreshGravity(context.Background(), dir, res)
	if err != nil {
		t.Fatal(err)
	}

	if !res.Resolve("ads.test.com") {
		t.Error("ads.test.com should be blocked after gravity sync")
	}

	if _, err := os.Stat(filepath.Join(dir, "gravity-0.cache")); os.IsNotExist(err) {
		t.Error("gravity.cache should have been created")
	}
}

// 1. Test corrupted gravity.state.json file recovery
func TestLoadState_CorruptedFileRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "gravity.state.json")

	// Inject malformed JSON
	corruptedData := []byte(`{"last_update": "missing_quote, "version": 2}`)
	if err := os.WriteFile(stateFile, corruptedData, 0644); err != nil {
		t.Fatalf("Failed to write corrupted state file: %v", err)
	}

	state := loadStateMap(stateFile)
	if len(state) != 0 {
		t.Errorf("Expected empty state on corrupted JSON, got: %v", state)
	}
}

// 2. Test a mixed 200 and 304 blocklist load (mock two servers)
func TestUpdate_Mixed200And304(t *testing.T) {
	// Mock Server 1: Returns 200 OK with new blocklist data
	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "new-etag")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0.0.0.0 domain200.com\n"))
	}))
	defer srv200.Close()

	// Mock Server 2: Returns 304 Not Modified
	srv304 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv304.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv200.URL, srv304.URL} // Override for test

	// Create a dummy cache file for the 304 server so it has something to read
	cache304Path := filepath.Join(dir, "gravity-1.cache")
	if err := os.WriteFile(cache304Path, []byte("domain304.com\n"), 0644); err != nil {
		t.Fatalf("Failed to create cache for 304 server: %v", err)
	}

	// Set state map to simulate cached ETag
	statePath := filepath.Join(dir, "gravity.state.json")
	saveStateMap(statePath, map[string]GravityState{
		srv304.URL: {ETag: "old-etag"},
	})

	res := NewFilterEngine(nil)

	err := refreshGravity(context.Background(), dir, res)
	if err != nil {
		t.Fatalf("Expected successful update, got error: %v", err)
	}

	if !res.Resolve("domain200.com") {
		t.Error("domain200.com should be blocked from the 200 response")
	}
	if !res.Resolve("domain304.com") {
		t.Error("domain304.com should be blocked from the 304 cached response")
	}
}

// 3. Test injecting a custom parser via RegisterParserForURL() and ensure execution
type mockParser struct {
	executed bool
}

func (m *mockParser) Parse(r io.Reader, onDomain func(string)) error {
	m.executed = true
	onDomain("custom-parsed-domain.com")
	return nil
}

func TestRegisterParserForURL_CustomParserExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dummy data"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL} // Override for test

	customParser := &mockParser{}
	RegisterParserForURL(server.URL, customParser)

	res := NewFilterEngine(nil)
	err := refreshGravity(context.Background(), dir, res)
	if err != nil {
		t.Fatalf("Unexpected error during parsing: %v", err)
	}

	if !customParser.executed {
		t.Error("Expected custom parser to be executed, but it was bypassed")
	}

	if !res.Resolve("custom-parsed-domain.com") {
		t.Error("Expected domain from custom parser to be blocked")
	}
}

// 4. Test longest-prefix match precedence for custom parsers
func TestRegisterParserForURL_LongestPrefixMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dummy data"))
	}))
	defer server.Close()

	dir := t.TempDir()

	// Setup a URL that matches both prefixes but has a longer matching prefix
	listURL := server.URL + "/list/gravity.txt"
	DefaultLists = []string{listURL}

	shortParser := &mockParser{}
	longParser := &mockParser{}

	// Register both parsers; mathematically the longest prefix should win
	RegisterParserForURL(server.URL+"/", shortParser)
	RegisterParserForURL(server.URL+"/list/", longParser)

	res := NewFilterEngine(nil)
	err := refreshGravity(context.Background(), dir, res)
	if err != nil {
		t.Fatalf("Unexpected error during parsing: %v", err)
	}

	if shortParser.executed {
		t.Error("Expected short prefix parser to NOT be executed")
	}
	if !longParser.executed {
		t.Error("Expected long prefix parser to be executed (longest prefix wins)")
	}
}

func TestStartGravitySync_Lifecycle(t *testing.T) {
	dir := t.TempDir()
	res := NewFilterEngine(nil)

	// Test case 1: invalid lists lead to error
	DefaultLists = []string{"http://invalid.local"} // Will fail to resolve/connect
	err := StartGravitySync(context.Background(), dir, res)
	if err == nil {
		t.Fatal("Expected error on invalid lists, got nil")
	}

	// Test case 2: valid lists sync correctly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 ads.test.com\n"))
	}))
	defer server.Close()

	DefaultLists = []string{server.URL}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = StartGravitySync(ctx, dir, res)
	if err != nil {
		t.Fatalf("Expected nil error on valid list, got: %v", err)
	}

	if !res.Resolve("ads.test.com") {
		t.Error("ads.test.com should be blocked")
	}

	cancel()
}

func TestRefreshGravity_MalformedURL(t *testing.T) {
	dir := t.TempDir()
	res := NewFilterEngine(nil)

	DefaultLists = []string{"://invalid-url-scheme"}

	err := refreshGravity(context.Background(), dir, res)
	if err == nil {
		t.Fatal("Expected error on malformed URL, got nil")
	}
	if err.Error() != "no gravity lists available" {
		t.Errorf("Expected 'no gravity lists available', got %v", err)
	}
}

// 5. Test scanner error does not corrupt existing trie
type badParser struct {
	domain string
}

func (b *badParser) Parse(r io.Reader, onDomain func(string)) error {
	onDomain(b.domain)
	return nil
}

func TestRefreshGravity_ScannerErrorDoesNotCorruptTrie(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dummy data"))
	}))
	defer server.Close()

	DefaultLists = []string{server.URL}

	// Initialize FilterEngine with an existing rule
	res := NewFilterEngine(nil)
	scanner := bufio.NewScanner(strings.NewReader("existing-domain.com\n"))
	existingTrie := BuildTrieFromScanner(scanner)
	res.UpdateRoot(existingTrie)

	if !res.Resolve("existing-domain.com") {
		t.Fatal("existing-domain.com should be blocked initially")
	}

	// Inject a parser that returns a domain string longer than bufio.MaxScanTokenSize (65536)
	longDomain := ""
	for i := 0; i < 65536+10; i++ {
		longDomain += "a"
	}
	longDomain += ".com"

	RegisterParserForURL(server.URL, &badParser{domain: longDomain})

	err := refreshGravity(context.Background(), dir, res)
	if err == nil {
		t.Fatal("Expected error due to scanner failure, got nil")
	}

	// Verify existing rule is still active
	if !res.Resolve("existing-domain.com") {
		t.Error("existing-domain.com should still be blocked; trie was corrupted!")
	}
}
