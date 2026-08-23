package dnsd

import (
	"bufio"
	"io"
	"strings"
)

// ListParser defines an interface for parsing different blocklist formats.
type ListParser interface {
	Parse(r io.Reader, onDomain func(string)) error
}

// BlocklistParser handles standard /etc/hosts and AdGuard formats.
type BlocklistParser struct{}

func (p *BlocklistParser) Parse(r io.Reader, onDomain func(string)) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if idx := strings.IndexAny(line, "#!"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "||") && strings.HasSuffix(line, "^") {
			line = line[2 : len(line)-1]
		}

		fields := strings.Fields(line)
		if len(fields) > 1 {
			if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" {
				line = fields[1]
			} else {
				continue
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

// PiHoleParser handles Pi-hole gravity lists (often just domains or IPs).
type PiHoleParser struct{}

func (p *PiHoleParser) Parse(r io.Reader, onDomain func(string)) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if idx := strings.IndexAny(line, "#!"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) > 0 {
			line = fields[len(fields)-1]
		}

		line = strings.ToLower(line)
		line = strings.TrimSuffix(line, ".")
		
		if line != "" && line != "0.0.0.0" && line != "127.0.0.1" {
			onDomain(line)
		}
	}
	return scanner.Err()
}

