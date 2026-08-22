package dnsd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func assertEventually(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

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

	assertEventually(t, func() bool {
		return !res.Resolve("good.com") && res.Resolve("bad.com")
	}, "initial list loading failed")

	// Test hot reload
	os.WriteFile(wlPath, []byte("good.com\nnewgood.com\n"), 0644)

	assertEventually(t, func() bool {
		return !res.Resolve("newgood.com")
	}, "newgood.com should be allowed after reload")
}
