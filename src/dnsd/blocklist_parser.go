package dnsd

import (
	"bufio"
	"io"
	"strings"
)

func ParseBlocklist(r io.Reader) []string {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
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
		domains = append(domains, line)
	}
	return domains
}
