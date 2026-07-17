package dnsd

import (
	"testing"
	"time"
)

func TestVPNMonitorInterface(t *testing.T) {
	triggered := false
	StartVPNMonitor(func(servers []string) {
		triggered = true
	})
	defer StopVPNMonitor()

	// Wait briefly to make sure goroutine starts but doesn't trigger initial callback
	time.Sleep(50 * time.Millisecond)

	if triggered {
		t.Error("Callback should only trigger on network changes, not on startup/registry")
	}
}

func TestVPNMonitorStop(t *testing.T) {
	StartVPNMonitor(func(servers []string) {})
	StopVPNMonitor()
}

func TestVPNMonitorConcurrent(t *testing.T) {
	// Start & Stop multiple times concurrently to verify no race conditions/panics
	for i := 0; i < 5; i++ {
		go func() {
			StartVPNMonitor(func(servers []string) {})
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
