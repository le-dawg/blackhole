package dnsd

import (
	"os"
	"path/filepath"
)

type Config struct {
	Port           int
	DataDir        string
	ExclusionsPath string
	SocketPath     string
}

func DefaultConfig() Config {
	dataDir := "/var/root/Library/Application Support/blackhole"
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, "Library/Application Support/blackhole")
	}

	return Config{
		Port:           5353,
		DataDir:        dataDir,
		ExclusionsPath: filepath.Join(dataDir, "exclusions.json"),
		SocketPath:     "/tmp/blackhole.sock",
	}
}
