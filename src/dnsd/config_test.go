package dnsd

import "testing"

func TestConfigSetExclusionsPathAlignsDataDir(t *testing.T) {
	cfg := DefaultConfig()

	cfg.SetExclusionsPath("/Users/testuser/Library/Application Support/blackhole/exclusions.json")

	if cfg.ExclusionsPath != "/Users/testuser/Library/Application Support/blackhole/exclusions.json" {
		t.Fatalf("expected exclusions path override to stick, got %q", cfg.ExclusionsPath)
	}
	if cfg.DataDir != "/Users/testuser/Library/Application Support/blackhole" {
		t.Fatalf("expected data dir to follow exclusions path, got %q", cfg.DataDir)
	}
}
