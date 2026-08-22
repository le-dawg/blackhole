package dnsd

import (
	"bufio"
	"io"
	"strings"
)

func ParseBlocklist(r io.Reader, onDomain func(string)) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Strip comments
		if idx := strings.IndexAny(line, "#!"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		if line == "" {
			continue
		}

		// Handle ABP/AdGuard syntax
		if strings.HasPrefix(line, "||") && strings.HasSuffix(line, "^") {
			line = line[2 : len(line)-1]
		}

		// Handle hosts file format
		fields := strings.Fields(line)
		if len(fields) > 1 {
			if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" {
				line = fields[1]
			} else {
				continue // Skip mapping to other IPs
			}
		}

		line = strings.ToLower(line)
		line = strings.TrimSuffix(line, ".")
		
		if line != "" {
			onDomain(line)
		}
	}
	return scanner.Err()
}
