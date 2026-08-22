package dnsd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserListWatcher(t *testing.T) {
	dir := t.TempDir()
	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	os.WriteFile(wlPath, []byte("good.com\n"), 0644)
	os.WriteFile(blPath, []byte("bad.com\n"), 0644)

	res := NewResolver(nil)
	watcher, err := StartUserListWatcher(dir, res)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	time.Sleep(100 * time.Millisecond) // Allow initial load

	if res.Resolve("good.com") {
		t.Error("good.com should be allowed")
	}
	if !res.Resolve("bad.com") {
		t.Error("bad.com should be blocked")
	}

	// Test hot reload
	os.WriteFile(wlPath, []byte("good.com\nnewgood.com\n"), 0644)
	time.Sleep(100 * time.Millisecond)

	if res.Resolve("newgood.com") {
		t.Error("newgood.com should be allowed after reload")
	}
}
