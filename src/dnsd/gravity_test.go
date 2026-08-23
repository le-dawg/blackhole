package dnsd

import (
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
	
	if _, err := os.Stat(filepath.Join(dir, "gravity.cache")); os.IsNotExist(err) {
		t.Error("gravity.cache should have been created")
	}
}
