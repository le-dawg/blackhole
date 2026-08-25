package dnsd

import (
	"strings"
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

	res := NewFilterEngine(nil)
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

func TestUserListWatcher_PreservesLastKnownGoodOnReloadFailure(t *testing.T) {
	dir := t.TempDir()
	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	if err := os.WriteFile(wlPath, []byte("good.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blPath, []byte("bad.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res := NewFilterEngine(nil)
	watcher, err := StartUserListWatcher(dir, res)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	assertEventually(t, func() bool {
		return !res.Resolve("good.com") && res.Resolve("bad.com")
	}, "initial list loading failed")

	oversizedLine := strings.Repeat("a", 70_000) + "\n"
	if err := os.WriteFile(blPath, []byte(oversizedLine), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if !res.Resolve("bad.com") {
		t.Fatal("expected last known-good blacklist entry to remain active after reload failure")
	}
}

func TestUserListWatcher_RapidWritesDoNotRevertToStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	wlPath := filepath.Join(dir, "whitelist.txt")
	blPath := filepath.Join(dir, "blacklist.txt")

	if err := os.WriteFile(wlPath, []byte("good.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blPath, []byte("bad-v1.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res := NewFilterEngine(nil)
	watcher, err := StartUserListWatcher(dir, res)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	assertEventually(t, func() bool {
		return res.Resolve("bad-v1.com")
	}, "initial blacklist loading failed")

	originalLoadListFunc := loadListFunc
	defer func() { setLoadListFuncForTest(originalLoadListFunc) }()

	firstBlacklistReadDone := make(chan struct{})
	releaseFirstBlacklistRead := make(chan struct{})
	secondBlacklistReadDone := make(chan struct{})
	blacklistLoadCalls := 0

	setLoadListFuncForTest(func(path string) (map[string]bool, error) {
		if path != blPath {
			return originalLoadListFunc(path)
		}

		blacklistLoadCalls++
		if blacklistLoadCalls == 1 {
			m, err := originalLoadListFunc(path)
			if err != nil {
				return nil, err
			}
			close(firstBlacklistReadDone)
			<-releaseFirstBlacklistRead
			return m, nil
		}

		m, err := originalLoadListFunc(path)
		if err == nil && blacklistLoadCalls == 2 {
			close(secondBlacklistReadDone)
		}
		return m, err
	})

	if err := os.WriteFile(blPath, []byte("bad-v2.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	<-firstBlacklistReadDone

	if err := os.WriteFile(blPath, []byte("bad-v3.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	close(releaseFirstBlacklistRead)
	<-secondBlacklistReadDone

	assertEventually(t, func() bool {
		return res.Resolve("bad-v3.com") && !res.Resolve("bad-v2.com")
	}, "expected latest blacklist snapshot to win after rapid successive writes")
}
