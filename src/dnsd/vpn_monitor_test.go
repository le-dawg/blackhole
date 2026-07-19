package dnsd

import (
	"testing"
	"time"
)

func TestVPNMonitorInterface(t *testing.T) {
	triggered := false
	err := StartVPNMonitor(func(servers []string) {
		triggered = true
	})
	if err != nil {
		t.Fatalf("StartVPNMonitor failed: %v", err)
	}
	defer StopVPNMonitor()

	// Wait briefly to make sure goroutine starts but doesn't trigger initial callback
	time.Sleep(50 * time.Millisecond)

	if triggered {
		t.Error("Callback should only trigger on network changes, not on startup/registry")
	}
}

func TestVPNMonitorStop(t *testing.T) {
	err := StartVPNMonitor(func(servers []string) {})
	if err != nil {
		t.Fatalf("StartVPNMonitor failed: %v", err)
	}
	StopVPNMonitor()
}

func TestVPNMonitorConcurrent(t *testing.T) {
	// Start & Stop multiple times concurrently to verify no race conditions/panics
	for i := 0; i < 5; i++ {
		go func() {
			_ = StartVPNMonitor(func(servers []string) {})
		}()
		go func() {
			StopVPNMonitor()
		}()
	}
	// Wait to let goroutines complete
	time.Sleep(50 * time.Millisecond)
	// Clean up at the end
	StopVPNMonitor()
}
