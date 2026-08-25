package dnsd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

	if _, err := os.Stat(cachePathForURL(dir, server.URL)); os.IsNotExist(err) {
		t.Error("gravity.cache should have been created")
	}
}

func TestRefreshGravity_CreatesMissingDataDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 ads.test.com\n"))
	}))
	defer server.Close()

	parentDir := t.TempDir()
	dir := filepath.Join(parentDir, "missing", "blackhole")
	DefaultLists = []string{server.URL}

	res := NewFilterEngine(nil)

	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to create missing data dir, got %v", err)
	}

	if !res.Resolve("ads.test.com") {
		t.Fatal("ads.test.com should be blocked after gravity sync")
	}

	if _, err := os.Stat(cachePathForURL(dir, server.URL)); err != nil {
		t.Fatalf("expected gravity cache in newly created dir, got %v", err)
	}
}

func TestSaveStateMap_DoesNotFollowTempSymlink(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	tempPath := statePath + ".tmp"
	sentinelPath := filepath.Join(dir, "sentinel.txt")

	if err := os.WriteFile(sentinelPath, []byte("do-not-touch"), 0644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}
	if err := os.Symlink(sentinelPath, tempPath); err != nil {
		t.Fatalf("failed to create temp symlink: %v", err)
	}

	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag"},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}

	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("failed to read sentinel: %v", err)
	}
	if string(data) != "do-not-touch" {
		t.Fatalf("expected sentinel to remain untouched, got %q", string(data))
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
	cache304Path := cachePathForURL(dir, srv304.URL)
	if err := os.WriteFile(cache304Path, []byte("domain304.com\n"), 0644); err != nil {
		t.Fatalf("Failed to create cache for 304 server: %v", err)
	}

	// Set state map to simulate cached ETag
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveStateMap(statePath, map[string]GravityState{
		srv304.URL: {ETag: "old-etag"},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}

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
	if !strings.Contains(err.Error(), "incomplete gravity sources") {
		t.Errorf("Expected 'incomplete gravity sources', got %v", err)
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

func TestRefreshGravity_PartialSourceFailurePreservesExistingTrie(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 bad1.com\n"))
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 bad2.com\n"))
	}))
	defer s2.Close()

	dir := t.TempDir()
	DefaultLists = []string{s1.URL, s2.URL}

	res := NewFilterEngine(nil)

	err := refreshGravity(context.Background(), dir, res)
	if err != nil {
		t.Fatalf("Expected successful initial update, got error: %v", err)
	}

	if !res.Resolve("bad1.com") || !res.Resolve("bad2.com") {
		t.Error("bad1.com and bad2.com should be blocked initially")
	}

	DefaultLists = []string{s1.URL, "http://invalid-missing-server-12345.local"}
	newDir := t.TempDir()
	err = refreshGravity(context.Background(), newDir, res)
	if err == nil {
		t.Fatal("Expected error due to partial source failure, got nil")
	}
	if !res.Resolve("bad1.com") || !res.Resolve("bad2.com") {
		t.Error("bad1.com and bad2.com should still be blocked; trie was corrupted!")
	}
}

func TestRefreshGravity_DoesNotFollowTempCacheSymlink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 ads.test.com\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	sentinelPath := filepath.Join(dir, "sentinel.txt")
	tempCachePath := cachePathForURL(dir, server.URL) + ".tmp"
	DefaultLists = []string{server.URL}

	if err := os.WriteFile(sentinelPath, []byte("do-not-touch"), 0644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}
	if err := os.Symlink(sentinelPath, tempCachePath); err != nil {
		t.Fatalf("failed to create temp cache symlink: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to succeed, got %v", err)
	}

	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("failed to read sentinel: %v", err)
	}
	if string(data) != "do-not-touch" {
		t.Fatalf("expected sentinel to remain untouched, got %q", string(data))
	}
}

func TestRefreshGravity_AdGuardExceptionSubtractsBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("||data.notify.macys.com^\n@@||data.notify.macys.com^|\n||keepblocked.example^\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL}
	RegisterParserForURL(server.URL, &BlocklistParser{})

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to succeed, got %v", err)
	}

	if res.Resolve("data.notify.macys.com") {
		t.Fatal("expected exception rule to suppress matching block rule")
	}
	if !res.Resolve("keepblocked.example") {
		t.Fatal("expected remaining effective block rule to stay active")
	}
}

func TestRefreshGravity_ExceptionOnlySourceFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("@@||ads.example.com^\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &BlocklistParser{})

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected exception-only source to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after exception-only source")
	}
}

func TestRefreshGravity_ExceptionOnlyCachedSourceFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-exception-only", Exceptions: []string{"ads.example.com"}},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}
	cachePath := cachePathForURL(dir, srv.URL)
	if err := os.WriteFile(cachePath, []byte("\n"), 0644); err != nil {
		t.Fatalf("failed to seed empty cache: %v", err)
	}

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected exception-only cached source to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after exception-only cached source")
	}
}

func TestRefreshGravity_CacheIdentityFollowsSourceURL(t *testing.T) {
	requestMode := "initial"
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "srv1-etag")
			w.Write([]byte("0.0.0.0 domain1.example\n"))
		default:
			t.Fatalf("srv1 should not be requested in mode %s", requestMode)
		}
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "srv2-etag")
			w.Write([]byte("0.0.0.0 domain2.example\n"))
		case "reordered":
			if got := r.Header.Get("If-None-Match"); got != "srv2-etag" {
				t.Fatalf("expected srv2 ETag on reordered fetch, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv2.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv1.URL, srv2.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refreshGravity to succeed, got %v", err)
	}
	if !res.Resolve("domain1.example") || !res.Resolve("domain2.example") {
		t.Fatal("expected initial caches to block both domains")
	}

	requestMode = "reordered"
	DefaultLists = []string{srv2.URL}
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected reordered refreshGravity to succeed, got %v", err)
	}

	if !res.Resolve("domain2.example") {
		t.Fatal("expected srv2 domain to remain blocked after source reorder")
	}
	if res.Resolve("domain1.example") {
		t.Fatal("did not expect srv1 cache to be reused for reordered srv2 source")
	}
}

func TestRefreshGravity_LegacyCacheFallbackOnNotModified(t *testing.T) {
	originalLegacyDefaultLists := append([]string(nil), legacyDefaultLists...)
	defer func() { legacyDefaultLists = originalLegacyDefaultLists }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "legacy-etag" {
			t.Fatalf("expected legacy ETag, got %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	legacyDefaultLists = []string{srv.URL}
	url := srv.URL

	dir := t.TempDir()
	DefaultLists = []string{url}

	legacyCachePath := filepath.Join(dir, "gravity-0.cache")
	if err := os.WriteFile(legacyCachePath, []byte("domain-legacy.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed legacy cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		url: {ETag: "legacy-etag"},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to use legacy cache on 304, got %v", err)
	}

	if !res.Resolve("domain-legacy.example") {
		t.Fatal("expected legacy cache domain to remain blocked")
	}
	if _, err := os.Stat(cachePathForURL(dir, url)); err != nil {
		t.Fatalf("expected legacy cache to migrate to hashed path, got %v", err)
	}
}

func TestOpenCacheForSource_MigratesLegacyCache(t *testing.T) {
	dir := t.TempDir()
	url := legacyDefaultLists[0]
	legacyCachePath := filepath.Join(dir, "gravity-0.cache")

	if err := os.WriteFile(legacyCachePath, []byte("domain-legacy.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed legacy cache: %v", err)
	}

	f, err := openCacheForSource(dir, url, 0)
	if err != nil {
		t.Fatalf("expected legacy cache migration to succeed, got %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read migrated cache: %v", err)
	}
	if string(data) != "domain-legacy.example\n" {
		t.Fatalf("expected migrated cache contents, got %q", string(data))
	}
}

func TestOpenCacheForSource_DoesNotReuseWrongLegacyCache(t *testing.T) {
	dir := t.TempDir()
	legacyCachePath := filepath.Join(dir, "gravity-0.cache")

	if err := os.WriteFile(legacyCachePath, []byte("wrong-domain.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed legacy cache: %v", err)
	}

	if _, err := openCacheForSource(dir, "https://replacement.example/list.txt", 0); err == nil {
		t.Fatal("expected unmatched replacement URL to reject legacy positional cache")
	}
}

func TestOpenCacheForSource_RestoresBackupWhenPrimaryMissing(t *testing.T) {
	dir := t.TempDir()
	url := legacyDefaultLists[0]
	cachePath := cachePathForURL(dir, url)
	backupPath := cachePath + ".bak"

	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}

	f, err := openCacheForSource(dir, url, 0)
	if err != nil {
		t.Fatalf("expected backup recovery to succeed, got %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read recovered cache: %v", err)
	}
	if string(data) != "old.example\n" {
		t.Fatalf("expected backup cache contents, got %q", string(data))
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected live cache to be restored from backup, got %v", err)
	}
}

func TestOpenCacheForSource_PrefersLiveCacheWhenBackupAlsoExists(t *testing.T) {
	dir := t.TempDir()
	url := legacyDefaultLists[0]
	cachePath := cachePathForURL(dir, url)
	backupPath := cachePath + ".bak"

	if err := os.WriteFile(cachePath, []byte("new.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed live cache: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}

	f, err := openCacheForSource(dir, url, 0)
	if err != nil {
		t.Fatalf("expected backup-preferred recovery to succeed, got %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read recovered cache: %v", err)
	}
	if string(data) != "new.example\n" {
		t.Fatalf("expected live cache contents to win when no journal is present, got %q", string(data))
	}
}

func TestRefreshGravity_PersistsExceptionsAcrossNotModified(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.Header().Set("ETag", "etag-1")
			w.Write([]byte("||example.com^\n@@||ads.example.com^\n"))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != "etag-1" {
			t.Fatalf("expected ETag etag-1, got %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &BlocklistParser{})

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refreshGravity to succeed, got %v", err)
	}
	if res.Resolve("ads.example.com") {
		t.Fatal("expected initial exception to allow ads.example.com")
	}

	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected 304 refreshGravity to succeed, got %v", err)
	}
	if res.Resolve("ads.example.com") {
		t.Fatal("expected persisted exception to survive 304 cache replay")
	}
}

type exceptionLeakParser struct{}

func (p *exceptionLeakParser) Parse(r io.Reader, onDomain func(string)) error {
	return nil
}

func (p *exceptionLeakParser) ParseRules(r io.Reader, onBlock func(string), onException func(string)) error {
	onException("ads.example.com")
	return io.ErrUnexpectedEOF
}

func TestRefreshGravity_FailedParseDoesNotLeakNewExceptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 example.com\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refreshGravity to succeed, got %v", err)
	}
	if res.Resolve("ads.example.com") != true {
		t.Fatal("expected initial parent block to cover ads.example.com")
	}

	RegisterParserForURL(srv.URL, &exceptionLeakParser{})
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected fallback refreshGravity to succeed, got %v", err)
	}
	if !res.Resolve("ads.example.com") {
		t.Fatal("did not expect failed parse exception state to leak into fallback trie")
	}
}

func TestRefreshGravity_EmptySuccessfulSourcePreservesExistingTrie(t *testing.T) {
	responseBody := "0.0.0.0 keep.example\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(responseBody))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refreshGravity to succeed, got %v", err)
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected initial block to be active")
	}

	responseBody = ""
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected empty successful source to fall back safely, got %v", err)
	}
	if !res.Resolve("keep.example") {
		t.Fatal("did not expect empty source to wipe existing protection")
	}
}

func TestRefreshGravity_FailedParseDoesNotPoisonSubsequent304Replay(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-keep")
			w.Write([]byte("0.0.0.0 keep.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-keep" {
				t.Fatalf("expected etag-keep, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refreshGravity to succeed, got %v", err)
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected initial block to be active")
	}

	longDomain := strings.Repeat("a", 65536+10) + ".com"
	RegisterParserForURL(srv.URL, &badParser{domain: longDomain})
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected fallback refreshGravity to succeed with previous cache, got %v", err)
	}

	RegisterParserForURL(srv.URL, &PiHoleParser{})
	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected subsequent 304 replay to succeed with last good cache, got %v", err)
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected last good cache to survive failed parse and later 304 replay")
	}
}

func TestRefreshGravity_FetchFailureUsesSharedFallbackAndExceptions(t *testing.T) {
	dir := t.TempDir()
	url := "http://127.0.0.1:1/unreachable"
	DefaultLists = []string{url}

	cachePath := cachePathForURL(dir, url)
	if err := os.WriteFile(cachePath, []byte("example.com\n"), 0644); err != nil {
		t.Fatalf("failed to seed cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		url: {Exceptions: []string{"ads.example.com"}},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to use shared fallback on fetch failure, got %v", err)
	}
	if !res.Resolve("example.com") {
		t.Fatal("expected cached blocked domain to remain active after fetch failure")
	}
	if res.Resolve("ads.example.com") {
		t.Fatal("expected persisted exception to be replayed on fetch failure")
	}
}

func TestRefreshGravity_EmptyCacheFallbackDoesNotCountAsValidSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-empty"},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}
	cachePath := cachePathForURL(dir, srv.URL)
	if err := os.WriteFile(cachePath, []byte("\n\n"), 0644); err != nil {
		t.Fatalf("failed to seed empty cache: %v", err)
	}

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected empty cache fallback to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected previous trie to remain active after empty cache fallback failure")
	}
}

func TestRefreshGravity_WhitespaceOnlyPersistedExceptionsDoNotValidateEmptyCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-empty", Exceptions: []string{"   ", "\t", ""}},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}
	cachePath := cachePathForURL(dir, srv.URL)
	if err := os.WriteFile(cachePath, []byte("\n"), 0644); err != nil {
		t.Fatalf("failed to seed empty cache: %v", err)
	}

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected whitespace-only persisted exceptions to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected previous trie to remain active after invalid persisted exception fallback")
	}
}

func TestRefreshGravity_MalformedPersistedExceptionsDoNotValidateEmptyCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-empty", Exceptions: []string{"not a domain", "slash/example", "<html>"}},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}
	cachePath := cachePathForURL(dir, srv.URL)
	if err := os.WriteFile(cachePath, []byte("\n"), 0644); err != nil {
		t.Fatalf("failed to seed empty cache: %v", err)
	}

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected malformed persisted exceptions to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected previous trie to remain active after malformed persisted exception fallback")
	}
}

type whitespaceParser struct{}

func (p *whitespaceParser) Parse(r io.Reader, onDomain func(string)) error {
	onDomain("   ")
	return nil
}

func TestRefreshGravity_WhitespaceOnlyParserOutputFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("irrelevant"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &whitespaceParser{})

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected whitespace-only parser output to fail closed")
	}
}

func TestRefreshGravity_GarbageSourceOutputFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>temporary error</html>\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &PiHoleParser{})

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected garbage source output to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after garbage source output")
	}
}

func TestRefreshGravity_PlainTextErrorBodyFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("temporary error\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &PiHoleParser{})

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected plain-text error body to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after plain-text error body")
	}
	if res.Resolve("error") {
		t.Fatal("did not expect plain-text error token to become a valid block rule")
	}
}

func TestRefreshGravity_PlainTextBodyWithOneDottedTokenFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("temporary outage status.example.com\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &PiHoleParser{})

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected incidental dotted token body to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after incidental dotted token body")
	}
	if res.Resolve("status.example.com") {
		t.Fatal("did not expect incidental dotted token to become a valid block rule")
	}
}

func TestRefreshGravity_SingleRuleRemoteBlocklistParserSourceFailsClosed(t *testing.T) {
	originalDefaultLists := DefaultLists
	originalLegacyDefaultLists := legacyDefaultLists
	originalTransport := http.DefaultTransport
	defer func() {
		DefaultLists = originalDefaultLists
		legacyDefaultLists = originalLegacyDefaultLists
		http.DefaultTransport = originalTransport
	}()

	url := "https://adguardteam.github.io/test-single"
	DefaultLists = []string{url}
	legacyDefaultLists = []string{url}
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("status.example.com\n")),
			Header:     make(http.Header),
		}, nil
	})

	dir := t.TempDir()
	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected single-rule remote BlocklistParser source to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after single-rule remote BlocklistParser source")
	}
	if res.Resolve("status.example.com") {
		t.Fatal("did not expect single remote dotted token to replace active protection")
	}
}

func TestRefreshGravity_TwoRuleRemoteBlocklistParserSourceSucceeds(t *testing.T) {
	originalDefaultLists := DefaultLists
	originalLegacyDefaultLists := legacyDefaultLists
	originalTransport := http.DefaultTransport
	defer func() {
		DefaultLists = originalDefaultLists
		legacyDefaultLists = originalLegacyDefaultLists
		http.DefaultTransport = originalTransport
	}()

	url := "https://adguardteam.github.io/test-double"
	DefaultLists = []string{url}
	legacyDefaultLists = []string{url}
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("one.example.com\ntwo.example.com\n")),
			Header:     make(http.Header),
		}, nil
	})

	dir := t.TempDir()
	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected two-rule remote BlocklistParser source to succeed, got %v", err)
	}
	if !res.Resolve("one.example.com") || !res.Resolve("two.example.com") {
		t.Fatal("expected both two-rule remote BlocklistParser domains to be blocked")
	}
}

func TestRefreshGravity_DuplicateRemotePiHoleRulesFailClosed(t *testing.T) {
	originalDefaultLists := DefaultLists
	originalLegacyDefaultLists := legacyDefaultLists
	originalTransport := http.DefaultTransport
	defer func() {
		DefaultLists = originalDefaultLists
		legacyDefaultLists = originalLegacyDefaultLists
		http.DefaultTransport = originalTransport
	}()

	url := "https://remote.example/test-duplicate"
	DefaultLists = []string{url}
	legacyDefaultLists = []string{url}
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("dup.example.com\ndup.example.com\n")),
			Header:     make(http.Header),
		}, nil
	})

	dir := t.TempDir()
	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected duplicate remote Pi-hole rules to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active after duplicate remote Pi-hole source")
	}
	if res.Resolve("dup.example.com") {
		t.Fatal("did not expect duplicate remote Pi-hole source to replace active protection")
	}
}

func TestRefreshGravity_SingleRuleLocalPiHoleSourceSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("single.example.com\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &PiHoleParser{})

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected single-rule localhost Pi-hole source to succeed, got %v", err)
	}
	if !res.Resolve("single.example.com") {
		t.Fatal("expected single-rule localhost Pi-hole source to block its domain")
	}
}

func TestRefreshGravity_TwoRulePiHoleSourceStillSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("one.example.com\ntwo.example.com\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &PiHoleParser{})

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected two-rule Pi-hole source to succeed, got %v", err)
	}
	if !res.Resolve("one.example.com") || !res.Resolve("two.example.com") {
		t.Fatal("expected both two-rule Pi-hole domains to be blocked")
	}
}

func TestRefreshGravity_StateSaveFailureDoesNotPoisonLaterReplay(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 keep.example\n0.0.0.0 keep2.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n0.0.0.0 new2.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-old" {
				t.Fatalf("expected old etag to survive failed publish, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}
	if !res.Resolve("keep.example") || !res.Resolve("keep2.example") {
		t.Fatal("expected initial cache to block keep.example")
	}

	oldSaveStateMapFunc := saveStateMapFunc
	saveStateMapFunc = func(path string, m map[string]GravityState) error {
		return errors.New("forced state save failure")
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected state save failure to surface")
	}
	saveStateMapFunc = oldSaveStateMapFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after failed publish to succeed, got %v", err)
	}
	if !res.Resolve("keep.example") || !res.Resolve("keep2.example") {
		t.Fatal("expected old cache/state pair to survive failed publish")
	}
	if res.Resolve("new.example") || res.Resolve("new2.example") {
		t.Fatal("did not expect failed publish to leave new cache contents active")
	}
}

func TestRefreshGravity_PostWriteStateSaveFailureKeepsNewState(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 keep.example\n0.0.0.0 keep2.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n0.0.0.0 new2.example\n"))
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldSaveStateMapFunc := saveStateMapFunc
	saveStateMapFunc = func(path string, m map[string]GravityState) error {
		if m[srv.URL].ETag == "etag-new" {
			if err := writeJSONFile(path, m); err != nil {
				return err
			}
			return errors.New("forced post-write state save failure")
		}
		return writeJSONFile(path, m)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected state save failure to surface")
	}
	saveStateMapFunc = oldSaveStateMapFunc

	state := loadStateMap(filepath.Join(dir, "gravity.state.json"))
	if state[srv.URL].ETag != "etag-new" {
		t.Fatalf("expected new state to remain after post-write state-save failure, got %q", state[srv.URL].ETag)
	}
	if !res.Resolve("new.example") || !res.Resolve("new2.example") {
		t.Fatal("expected new trie to remain active after post-write state-save failure")
	}
	if res.Resolve("keep.example") || res.Resolve("keep2.example") {
		t.Fatal("did not expect old trie generation to remain active after post-write state-save failure")
	}
}

func TestRefreshGravity_LaterCachePublishFailureRollsBackEarlierSources(t *testing.T) {
	requestMode := "initial"
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-1-old")
			w.Write([]byte("0.0.0.0 old1.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-1-new")
			w.Write([]byte("0.0.0.0 new1.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-1-old" {
				t.Fatalf("expected old etag for source 1 after rollback, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-2-old")
			w.Write([]byte("0.0.0.0 old2.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-2-new")
			w.Write([]byte("0.0.0.0 new2.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-2-old" {
				t.Fatalf("expected old etag for source 2 after rollback, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv2.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv1.URL, srv2.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}
	if !res.Resolve("old1.example") || !res.Resolve("old2.example") {
		t.Fatal("expected initial caches to be active")
	}

	publishRenames := 0
	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			publishRenames++
		}
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") && publishRenames == 2 {
			return errors.New("forced later cache publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	defer func() { renameFileFunc = oldRenameFileFunc }()

	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected later cache publish failure to surface")
	}

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after rollback to succeed, got %v", err)
	}
	if !res.Resolve("old1.example") || !res.Resolve("old2.example") {
		t.Fatal("expected old cache generation to survive later cache publish failure")
	}
	if res.Resolve("new1.example") || res.Resolve("new2.example") {
		t.Fatal("did not expect mixed new cache generation to survive failed publish")
	}
}

func TestRefreshGravity_LaterBackupFailureRollsBackEarlierSources(t *testing.T) {
	requestMode := "initial"
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-1-old")
			w.Write([]byte("0.0.0.0 old1.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-1-new")
			w.Write([]byte("0.0.0.0 new1.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-1-old" {
				t.Fatalf("expected old etag for source 1 after rollback, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-2-old")
			w.Write([]byte("0.0.0.0 old2.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-2-new")
			w.Write([]byte("0.0.0.0 new2.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-2-old" {
				t.Fatalf("expected old etag for source 2 after rollback, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv2.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv1.URL, srv2.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}
	if !res.Resolve("old1.example") || !res.Resolve("old2.example") {
		t.Fatal("expected initial caches to be active")
	}

	backupRenames := 0
	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.HasSuffix(newPath, ".bak") {
			backupRenames++
			if backupRenames == 2 {
				return errors.New("forced later backup failure")
			}
		}
		return os.Rename(oldPath, newPath)
	}
	defer func() { renameFileFunc = oldRenameFileFunc }()

	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected later backup failure to surface")
	}

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after rollback to succeed, got %v", err)
	}
	if !res.Resolve("old1.example") || !res.Resolve("old2.example") {
		t.Fatal("expected old cache generation to survive later backup failure")
	}
	if res.Resolve("new1.example") || res.Resolve("new2.example") {
		t.Fatal("did not expect mixed new cache generation to survive failed backup stage")
	}
}

func TestRefreshGravity_JournalRecoveryRestoresOldGeneration(t *testing.T) {
	requestMode := "304"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestMode != "304" {
			t.Fatalf("unexpected request mode %s", requestMode)
		}
		if got := r.Header.Get("If-None-Match"); got != "etag-old" {
			t.Fatalf("expected old etag after journal recovery, got %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	cachePath := cachePathForURL(dir, srv.URL)
	backupPath := cachePath + ".bak"
	if err := os.WriteFile(cachePath, []byte("new.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed live cache: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed old state map: %v", err)
	}
	if err := saveGravityPublishJournal(
		dir,
		map[string]GravityState{srv.URL: {ETag: "etag-old"}},
		map[string]GravityState{
			gravityStateMetaKey: {ETag: "commit-new"},
			srv.URL:             {ETag: "etag-new"},
		},
		[]gravityJournalCache{{CachePath: cachePath, HadPreviousCache: true}},
	); err != nil {
		t.Fatalf("failed to save publish journal: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected journal recovery refresh to succeed, got %v", err)
	}
	if !res.Resolve("old.example") {
		t.Fatal("expected old cache generation after journal recovery")
	}
	if res.Resolve("new.example") {
		t.Fatal("did not expect interrupted new cache generation to survive journal recovery")
	}
}

func TestRefreshGravity_PreInstallCrashJournalRestoresOldGeneration(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-old" {
				t.Fatalf("expected old etag after pre-install crash recovery, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			return errors.New("forced pre-install crash point")
		}
		return os.Rename(oldPath, newPath)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced pre-install failure to surface")
	}
	renameFileFunc = oldRenameFileFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after pre-install crash to succeed, got %v", err)
	}
	if !res.Resolve("old.example") {
		t.Fatal("expected old generation to survive pre-install crash")
	}
	if res.Resolve("new.example") {
		t.Fatal("did not expect new generation to survive pre-install crash")
	}
}

func TestRefreshGravity_PreInstallCrashWithUnchangedMetadataRollsBackOldGeneration(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 new.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-same" {
				t.Fatalf("expected unchanged etag after rollback, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			return errors.New("forced pre-install crash point")
		}
		return os.Rename(oldPath, newPath)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced pre-install failure to surface")
	}
	renameFileFunc = oldRenameFileFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after pre-install crash to succeed, got %v", err)
	}
	if !res.Resolve("old.example") {
		t.Fatal("expected old generation to survive unchanged-metadata pre-install crash")
	}
	if res.Resolve("new.example") {
		t.Fatal("did not expect new generation to survive unchanged-metadata pre-install crash")
	}
}

func TestRefreshGravity_RealPublishJournalRestoresOldGeneration(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-old" {
				t.Fatalf("expected old etag after journaled pre-install crash, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			return errors.New("forced first staged cache publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced pre-install publish failure")
	}
	renameFileFunc = oldRenameFileFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after real journaled pre-install crash to succeed, got %v", err)
	}
	if !res.Resolve("old.example") {
		t.Fatal("expected old generation to survive real journaled pre-install crash")
	}
	if res.Resolve("new.example") {
		t.Fatal("did not expect new generation to survive real journaled pre-install crash")
	}
}

func TestRefreshGravity_JournalStoresNextStateBeforePublish(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n"))
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			return errors.New("forced first staged cache publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced pre-install publish failure")
	}
	renameFileFunc = oldRenameFileFunc

	journalPath := gravityPublishJournalPath(dir)
	f, err := os.Open(journalPath)
	if err != nil {
		t.Fatalf("expected publish journal to remain after failed publish, got %v", err)
	}
	defer f.Close()

	var journal gravityPublishJournal
	if err := json.NewDecoder(f).Decode(&journal); err != nil {
		t.Fatalf("failed to decode publish journal: %v", err)
	}
	if journal.NextState[srv.URL].ETag != "etag-new" {
		t.Fatalf("expected journal next state to record etag-new, got %q", journal.NextState[srv.URL].ETag)
	}
}

func TestRecoverGravityArtifacts_EmptyJournalFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed state map: %v", err)
	}
	journalPath := gravityPublishJournalPath(dir)
	if err := os.WriteFile(journalPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to seed empty journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected empty journal to fail closed")
	}
	state := loadStateMap(statePath)
	if state["https://example.com/list.txt"].ETag != "etag-old" {
		t.Fatalf("expected existing state to survive empty journal, got %v", state)
	}
}

func TestRecoverGravityArtifacts_NullJournalFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed state map: %v", err)
	}
	journalPath := gravityPublishJournalPath(dir)
	if err := os.WriteFile(journalPath, []byte("null\n"), 0644); err != nil {
		t.Fatalf("failed to seed null journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected null journal to fail closed")
	}
	state := loadStateMap(statePath)
	if state["https://example.com/list.txt"].ETag != "etag-old" {
		t.Fatalf("expected existing state to survive null journal, got %v", state)
	}
}

func TestRecoverGravityArtifacts_EmptyObjectJournalFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed state map: %v", err)
	}
	journalPath := gravityPublishJournalPath(dir)
	if err := os.WriteFile(journalPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to seed empty-object journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected empty-object journal to fail closed")
	}
	state := loadStateMap(statePath)
	if state["https://example.com/list.txt"].ETag != "etag-old" {
		t.Fatalf("expected existing state to survive empty-object journal, got %v", state)
	}
}

func TestRecoverGravityArtifacts_JournalMissingCommitMarkerFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	cachePath := cachePathForURL(dir, "https://example.com/list.txt")
	backupPath := cachePath + ".bak"

	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}
	journalPath := gravityPublishJournalPath(dir)
	journal := gravityPublishJournal{
		PreviousState: map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		NextState:     map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		Caches:        []gravityJournalCache{{CachePath: cachePath, HadPreviousCache: true}},
	}
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatalf("failed to marshal journal: %v", err)
	}
	if err := os.WriteFile(journalPath, data, 0644); err != nil {
		t.Fatalf("failed to seed journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected journal missing commit marker to fail closed")
	}
}

func TestRecoverGravityArtifacts_JournalTrailingDataFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed state map: %v", err)
	}
	journalPath := gravityPublishJournalPath(dir)
	data := `{"previous_state":{"https://example.com/list.txt":{"etag":"etag-old"}},"next_state":{"__gravity_meta__":{"etag":"commit-1"},"https://example.com/list.txt":{"etag":"etag-old"}},"caches":[{"cache_path":"` + filepath.Join(dir, "gravity-test.cache") + `","had_previous_cache":true}]}{"trailing":true}`
	if err := os.WriteFile(journalPath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to seed trailing journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected trailing journal data to fail closed")
	}
}

func TestRecoverGravityArtifacts_BackupStatErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	cachePath := cachePathForURL(dir, "https://example.com/list.txt")
	backupPath := cachePath + ".bak"

	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}
	if err := saveGravityPublishJournal(
		dir,
		map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		map[string]GravityState{
			gravityStateMetaKey:            {ETag: "commit-1"},
			"https://example.com/list.txt": {ETag: "etag-old"},
		},
		[]gravityJournalCache{{CachePath: cachePath, HadPreviousCache: true}},
	); err != nil {
		t.Fatalf("failed to seed publish journal: %v", err)
	}

	oldStatFileFunc := statFileFunc
	statFileFunc = func(path string) (os.FileInfo, error) {
		if path == backupPath {
			return nil, errors.New("forced stat failure")
		}
		return os.Stat(path)
	}
	defer func() { statFileFunc = oldStatFileFunc }()

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected backup stat failure to fail closed")
	}
}

func TestRecoverGravityArtifacts_InvalidCachePathFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := saveGravityPublishJournal(
		dir,
		map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		map[string]GravityState{
			gravityStateMetaKey:            {ETag: "commit-1"},
			"https://example.com/list.txt": {ETag: "etag-old"},
		},
		[]gravityJournalCache{{CachePath: "../outside.cache", HadPreviousCache: true}},
	); err != nil {
		t.Fatalf("failed to seed publish journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected invalid cache path to fail closed")
	}
}

func TestRecoverGravityArtifacts_InvalidCacheBasenameFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	journalPath := gravityPublishJournalPath(dir)
	journal := gravityPublishJournal{
		PreviousState: map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		NextState: map[string]GravityState{
			gravityStateMetaKey:            {ETag: "commit-1"},
			"https://example.com/list.txt": {ETag: "etag-old"},
		},
		Caches: []gravityJournalCache{{CachePath: filepath.Join(dir, "gravity.state.json"), HadPreviousCache: true}},
	}
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatalf("failed to marshal journal: %v", err)
	}
	if err := os.WriteFile(journalPath, data, 0644); err != nil {
		t.Fatalf("failed to seed journal: %v", err)
	}

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected invalid cache basename to fail closed")
	}
}

func TestRecoverGravityArtifacts_JournalStatErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	journalPath := gravityPublishJournalPath(dir)
	if err := os.WriteFile(journalPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to seed journal: %v", err)
	}

	oldStatFileFunc := statFileFunc
	statFileFunc = func(path string) (os.FileInfo, error) {
		if path == journalPath {
			return nil, errors.New("forced journal stat failure")
		}
		return os.Stat(path)
	}
	defer func() { statFileFunc = oldStatFileFunc }()

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected journal stat failure to fail closed")
	}
}

func TestRecoverGravityArtifacts_CacheRemovalFailureFailsClosed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	cachePath := cachePathForURL(dir, "https://example.com/list.txt")

	if err := os.WriteFile(cachePath, []byte("new.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed live cache: %v", err)
	}
	if err := saveStateMap(statePath, map[string]GravityState{
		"https://example.com/list.txt": {ETag: "etag-old"},
	}); err != nil {
		t.Fatalf("failed to seed state map: %v", err)
	}
	if err := saveGravityPublishJournal(
		dir,
		map[string]GravityState{"https://example.com/list.txt": {ETag: "etag-old"}},
		map[string]GravityState{
			gravityStateMetaKey:            {ETag: "commit-new"},
			"https://example.com/list.txt": {ETag: "etag-new"},
		},
		[]gravityJournalCache{{CachePath: cachePath, HadPreviousCache: false}},
	); err != nil {
		t.Fatalf("failed to seed publish journal: %v", err)
	}

	oldRemoveFileFunc := removeFileFunc
	removeFileFunc = func(path string) error {
		if path == cachePath {
			return errors.New("forced cache removal failure")
		}
		return os.Remove(path)
	}
	defer func() { removeFileFunc = oldRemoveFileFunc }()

	if err := recoverGravityArtifacts(dir, statePath); err == nil {
		t.Fatal("expected cache-removal failure to fail closed")
	}
	if _, err := os.Stat(gravityPublishJournalPath(dir)); err != nil {
		t.Fatalf("expected publish journal to remain for manual recovery, got %v", err)
	}
	state := loadStateMap(statePath)
	if state["https://example.com/list.txt"].ETag != "etag-old" {
		t.Fatalf("expected old state to remain untouched on failed recovery, got %v", state)
	}
}

func TestRefreshGravity_JournalRemovalFailureKeepsNewGeneration(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-new" {
				t.Fatalf("expected new etag after journal-removal failure, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRemoveFileFunc := removeFileFunc
	removeFileFunc = func(path string) error {
		if path == gravityPublishJournalPath(dir) {
			return errors.New("forced journal removal failure")
		}
		return os.Remove(path)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced journal removal failure to surface")
	}
	removeFileFunc = oldRemoveFileFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after journal-removal failure to succeed, got %v", err)
	}
	if !res.Resolve("new.example") {
		t.Fatal("expected new generation to survive journal-removal failure")
	}
	if res.Resolve("old.example") {
		t.Fatal("did not expect old generation to override successful state save")
	}
}

func TestRefreshGravity_PostCommitCleanupFailureKeepsNewTrieActive(t *testing.T) {
	requestMode := "initial"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-old")
			w.Write([]byte("0.0.0.0 old.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-new")
			w.Write([]byte("0.0.0.0 new.example\n"))
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	oldRemoveFileFunc := removeFileFunc
	removeFileFunc = func(path string) error {
		if strings.HasSuffix(path, ".bak") {
			if _, err := os.Stat(path); err == nil {
				return errors.New("forced backup cleanup failure")
			}
		}
		return os.Remove(path)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected post-commit cleanup failure to surface")
	}
	removeFileFunc = oldRemoveFileFunc

	if !res.Resolve("new.example") {
		t.Fatal("expected new trie to remain active despite post-commit cleanup failure")
	}
	if res.Resolve("old.example") {
		t.Fatal("did not expect old trie to remain active after successful commit")
	}
}

func TestRefreshGravity_PostStateSaveCrashKeepsNewGeneration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "etag-new" {
			t.Fatalf("expected new etag after post-state-save crash, got %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	cachePath := cachePathForURL(dir, srv.URL)
	backupPath := cachePath + ".bak"
	if err := os.WriteFile(cachePath, []byte("new.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed live cache: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		srv.URL: {ETag: "etag-new"},
	}); err != nil {
		t.Fatalf("failed to save new state map: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected post-state-save crash recovery refresh to succeed, got %v", err)
	}
	if !res.Resolve("new.example") {
		t.Fatal("expected new generation to survive post-state-save crash")
	}
	if res.Resolve("old.example") {
		t.Fatal("did not expect stale backup cache to override live generation after state save")
	}
}

func TestRefreshGravity_PostStateSaveCrashWithUnchangedMetadataKeepsNewGeneration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "etag-same" {
			t.Fatalf("expected unchanged etag after committed crash, got %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	cachePath := cachePathForURL(dir, srv.URL)
	backupPath := cachePath + ".bak"
	if err := os.WriteFile(cachePath, []byte("new.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed live cache: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("old.example\n"), 0644); err != nil {
		t.Fatalf("failed to seed backup cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		gravityStateMetaKey: {ETag: "commit-same"},
		srv.URL:             {ETag: "etag-same"},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}
	if err := saveGravityPublishJournal(
		dir,
		map[string]GravityState{srv.URL: {ETag: "etag-same"}},
		map[string]GravityState{
			gravityStateMetaKey: {ETag: "commit-same"},
			srv.URL:             {ETag: "etag-same"},
		},
		[]gravityJournalCache{{CachePath: cachePath, HadPreviousCache: true}},
	); err != nil {
		t.Fatalf("failed to save publish journal: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected post-state-save crash recovery refresh to succeed, got %v", err)
	}
	if !res.Resolve("new.example") {
		t.Fatal("expected new generation to survive unchanged-metadata post-state-save crash")
	}
	if res.Resolve("old.example") {
		t.Fatal("did not expect old backup generation to override committed live cache")
	}
}

func TestRefreshGravity_MultiSourcePreInstallCrashWithUnchangedMetadataRollsBackAllSources(t *testing.T) {
	requestMode := "initial"
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 old1.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 new1.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-same" {
				t.Fatalf("expected unchanged etag for source 1, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requestMode {
		case "initial":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 old2.example\n"))
		case "update":
			w.Header().Set("ETag", "etag-same")
			w.Write([]byte("0.0.0.0 new2.example\n"))
		case "304":
			if got := r.Header.Get("If-None-Match"); got != "etag-same" {
				t.Fatalf("expected unchanged etag for source 2, got %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request mode %s", requestMode)
		}
	}))
	defer srv2.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv1.URL, srv2.URL}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected initial refresh to succeed, got %v", err)
	}

	publishRenames := 0
	oldRenameFileFunc := renameFileFunc
	renameFileFunc = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".tmp-") && strings.HasSuffix(newPath, ".cache") {
			publishRenames++
			if publishRenames == 2 {
				return errors.New("forced later cache publish failure")
			}
		}
		return os.Rename(oldPath, newPath)
	}
	requestMode = "update"
	if err := refreshGravity(context.Background(), dir, res); err == nil {
		t.Fatal("expected forced later publish failure")
	}
	renameFileFunc = oldRenameFileFunc

	requestMode = "304"
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected replay after multi-source pre-install crash to succeed, got %v", err)
	}
	if !res.Resolve("old1.example") || !res.Resolve("old2.example") {
		t.Fatal("expected old generations to survive multi-source pre-install crash")
	}
	if res.Resolve("new1.example") || res.Resolve("new2.example") {
		t.Fatal("did not expect mixed new generation to survive multi-source pre-install crash")
	}
}

func TestRefreshGravity_ReclaimsStaleTempArtifacts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 keep.example\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}

	cacheTemp := filepath.Join(dir, "gravity-deadbeef.cache.tmp-123")
	stateTemp := filepath.Join(dir, "gravity.state.json.tmp-123")
	if err := os.WriteFile(cacheTemp, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed stale cache temp: %v", err)
	}
	if err := os.WriteFile(stateTemp, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed stale state temp: %v", err)
	}

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refresh with stale temps to succeed, got %v", err)
	}
	if _, err := os.Stat(cacheTemp); !os.IsNotExist(err) {
		t.Fatalf("expected stale cache temp to be removed, got %v", err)
	}
	if _, err := os.Stat(stateTemp); !os.IsNotExist(err) {
		t.Fatalf("expected stale state temp to be removed, got %v", err)
	}
}

func TestLoadStateMap_NullYieldsEmptyMap(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	if err := os.WriteFile(statePath, []byte("null\n"), 0644); err != nil {
		t.Fatalf("failed to write null state file: %v", err)
	}

	state := loadStateMap(statePath)
	if state == nil {
		t.Fatal("expected null state file to produce empty map, not nil")
	}
	if len(state) != 0 {
		t.Fatalf("expected empty state map from null, got %v", state)
	}
}

func TestLoadStateMap_DecodeErrorDropsPartialEntries(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	data := `{"https://example.com/list.txt":{"etag":"etag","exceptions":["good.example"]},"broken":{"etag":123}}`
	if err := os.WriteFile(statePath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write malformed state file: %v", err)
	}

	state := loadStateMap(statePath)
	if len(state) != 0 {
		t.Fatalf("expected malformed state decode to drop partial entries, got %v", state)
	}
}

func TestLoadStateMap_TrailingGarbageDropsEntries(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gravity.state.json")
	data := `{"https://example.com/list.txt":{"etag":"etag","exceptions":["good.example"]}}{"trailing":true}`
	if err := os.WriteFile(statePath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write trailing-garbage state file: %v", err)
	}

	state := loadStateMap(statePath)
	if len(state) != 0 {
		t.Fatalf("expected trailing garbage to invalidate state file, got %v", state)
	}
}

func TestRefreshGravity_NoSourcesFailsClosed(t *testing.T) {
	originalDefaultLists := DefaultLists
	DefaultLists = nil
	defer func() { DefaultLists = originalDefaultLists }()

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("keep.example\n")))
	res.UpdateGravityData(existingTrie, nil)

	if err := refreshGravity(context.Background(), t.TempDir(), res); err == nil {
		t.Fatal("expected zero configured sources to fail closed")
	}
	if !res.Resolve("keep.example") {
		t.Fatal("expected existing trie to remain active when no sources are configured")
	}
}

func TestRefreshGravity_ReopenFailureDoesNotLeakExceptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("||example.com^\n@@||ads.example.com^\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	DefaultLists = []string{srv.URL}
	RegisterParserForURL(srv.URL, &BlocklistParser{})

	res := NewFilterEngine(nil)
	existingTrie := BuildTrieFromScanner(bufio.NewScanner(strings.NewReader("example.com\n")))
	res.UpdateGravityData(existingTrie, nil)
	cachePath := cachePathForURL(dir, srv.URL)
	if err := os.WriteFile(cachePath, []byte("example.com\n"), 0644); err != nil {
		t.Fatalf("failed to seed fallback cache: %v", err)
	}

	oldCreateSiblingTempFile := createSiblingTempFileFunc
	createSiblingTempFileFunc = func(path string) (*os.File, string, error) {
		f, tempPath, err := oldCreateSiblingTempFile(path)
		if err != nil {
			return nil, "", err
		}
		if err := os.Remove(tempPath); err != nil {
			return nil, "", err
		}
		return f, tempPath, nil
	}
	defer func() { createSiblingTempFileFunc = oldCreateSiblingTempFile }()

	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected reopen failure to fall back safely, got %v", err)
	}
	if !res.Resolve("ads.example.com") {
		t.Fatal("did not expect staged exception state to leak when staged cache reopen fails")
	}
}

func TestRefreshGravity_TempFileFailureUsesSharedFallbackAndExceptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("example.com\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL}

	cachePath := cachePathForURL(dir, server.URL)
	if err := os.WriteFile(cachePath, []byte("example.com\n"), 0644); err != nil {
		t.Fatalf("failed to seed cache: %v", err)
	}
	if err := saveStateMap(filepath.Join(dir, "gravity.state.json"), map[string]GravityState{
		server.URL: {Exceptions: []string{"ads.example.com"}},
	}); err != nil {
		t.Fatalf("failed to save state map: %v", err)
	}

	oldCreateSiblingTempFile := createSiblingTempFileFunc
	createSiblingTempFileFunc = func(path string) (*os.File, string, error) {
		return nil, "", errors.New("forced temp file failure")
	}
	defer func() {
		createSiblingTempFileFunc = oldCreateSiblingTempFile
	}()

	res := NewFilterEngine(nil)
	if err := refreshGravity(context.Background(), dir, res); err != nil {
		t.Fatalf("expected refreshGravity to use shared fallback on temp file failure, got %v", err)
	}
	if !res.Resolve("example.com") {
		t.Fatal("expected cached blocked domain to remain active after temp file failure")
	}
	if res.Resolve("ads.example.com") {
		t.Fatal("expected persisted exception to be replayed on temp file failure")
	}
}

func TestRefreshGravity_StatErrorAbortsPublish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bad1.com\nbad2.com\n"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL}

	oldStatFile := statFileFunc
	defer func() { statFileFunc = oldStatFile }()

	statFileFunc = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, ".cache") {
			return nil, errors.New("permission denied")
		}
		return os.Stat(name)
	}

	res := NewFilterEngine(nil)
	err := refreshGravity(context.Background(), dir, res)
	if err == nil {
		t.Fatal("expected refreshGravity to fail when stat encounters an unexpected error, got nil")
	}
	if !strings.Contains(err.Error(), "stat cache for") {
		t.Fatalf("expected stat error message, got: %v", err)
	}
}

