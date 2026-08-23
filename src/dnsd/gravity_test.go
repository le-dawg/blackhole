package dnsd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	
	err := refreshGravity(dir, res)
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
	
	err := refreshGravity(dir, res)
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

// 3. Test injecting a custom parser via SetParser() and ensure execution
type mockParser struct {
	executed bool
}

func (m *mockParser) Parse(r io.Reader, onDomain func(string)) error {
	m.executed = true
	onDomain("custom-parsed-domain.com")
	return nil
}

func TestSetParser_CustomParserExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dummy data"))
	}))
	defer server.Close()

	dir := t.TempDir()
	DefaultLists = []string{server.URL} // Override for test

	customParser := &mockParser{}
	originalParser := activeParser
	defer SetParser(originalParser)

	SetParser(customParser)

	res := NewFilterEngine(nil)
	err := refreshGravity(dir, res)
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
