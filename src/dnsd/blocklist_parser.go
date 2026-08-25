package dnsd

import (
	"bufio"
	"io"
	"os"
	"sort"
	"strings"
)

// ListParser defines an interface for parsing different blocklist formats.
type ListParser interface {
	Parse(r io.Reader, onDomain func(string)) error
}

type RuleAwareListParser interface {
	ParseRules(r io.Reader, onBlock func(string), onException func(string)) error
}

var blocklistParserProgressHook func(lineCount int)

// BlocklistParser handles standard /etc/hosts and AdGuard formats.
type BlocklistParser struct{}

var cosmeticRuleMarkers = []string{
	"##",
	"#@#",
	"#?#",
	"#$#",
	"#%#",
	"#@?#",
	"#@$#",
	"#@%#",
}

func (p *BlocklistParser) Parse(r io.Reader, onDomain func(string)) error {
	return p.ParseRules(r, onDomain, func(string) {})
}

func (p *BlocklistParser) ParseRules(r io.Reader, onBlock func(string), onException func(string)) error {
	scanner := bufio.NewScanner(r)
	blockExceptions := make(map[string]struct{})
	badFilters := make(map[string]struct{})
	exceptionsSeen := make(map[string]struct{})
	lineCount := 0
	rulesFile, err := os.CreateTemp("", "blackhole-blocklist-rules-*")
	if err != nil {
		return err
	}
	defer os.Remove(rulesFile.Name())
	defer rulesFile.Close()

	rulesWriter := bufio.NewWriter(rulesFile)
	writeRule := func(ruleKey, domain string) error {
		if domain == "" {
			return nil
		}
		_, err := rulesWriter.WriteString(ruleKey + "\t" + domain + "\n")
		return err
	}

	for scanner.Scan() {
		lineCount++
		if blocklistParserProgressHook != nil && lineCount%1000 == 0 {
			blocklistParserProgressHook(lineCount)
		}
		line := strings.TrimSpace(scanner.Text())

		if containsAny(line, cosmeticRuleMarkers) {
			continue
		}

		if idx := strings.IndexAny(line, "#!"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		if line == "" {
			continue
		}

		if domain, ruleKey, action, handled := parseAdGuardRule(line); handled {
			if domain == "" {
				continue
			}
			switch action {
			case adGuardBlock:
				if err := writeRule(ruleKey, domain); err != nil {
					return err
				}
			case adGuardException:
				blockExceptions[domain] = struct{}{}
				if _, seen := exceptionsSeen[domain]; !seen {
					exceptionsSeen[domain] = struct{}{}
					onException(domain)
				}
			case adGuardBadfilter:
				badFilters[ruleKey] = struct{}{}
			}
			continue
		}

		fields := strings.Fields(line)
		if len(fields) > 1 {
			if isHostsMappingIP(fields[0]) {
					for _, host := range fields[1:] {
						host = normalizeDomain(host)
						if host != "" {
							if err := writeRule(host, host); err != nil {
								return err
							}
						}
					}
				continue
			} else {
				continue
			}
		}

		line = normalizeDomain(line)

		if line != "" {
			if err := writeRule(line, line); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := rulesWriter.Flush(); err != nil {
		return err
	}
	if _, err := rulesFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	emitted := make(map[string]struct{})
	rulesScanner := bufio.NewScanner(rulesFile)
	for rulesScanner.Scan() {
		line := rulesScanner.Text()
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx == -1 {
			continue
		}
		ruleKey := line[:tabIdx]
		domain := line[tabIdx+1:]
		if _, suppressed := badFilters[ruleKey]; suppressed {
			continue
		}
		if _, excepted := blockExceptions[domain]; excepted {
			continue
		}
		if _, seen := emitted[domain]; seen {
			continue
		}
		emitted[domain] = struct{}{}
		if domain != "" {
			onBlock(domain)
		}
	}
	if err := rulesScanner.Err(); err != nil {
		return err
	}
	return nil
}

type adGuardRuleAction uint8

const (
	adGuardBlock adGuardRuleAction = iota
	adGuardException
	adGuardBadfilter
)

func parseAdGuardRule(line string) (domain string, ruleKey string, action adGuardRuleAction, handled bool) {
	switch {
	case strings.HasPrefix(line, "@@||"):
		domain, ruleKey, _ = extractAdGuardRule(line[4:])
		return domain, ruleKey, adGuardException, true
	case strings.HasPrefix(line, "||"):
		domain, ruleKey, hasBadfilter := extractAdGuardRule(line[2:])
		if domain == "" {
			return "", "", adGuardBlock, true
		}
		if hasBadfilter {
			return domain, ruleKey, adGuardBadfilter, true
		}
		return domain, ruleKey, adGuardBlock, true
	default:
		return "", "", adGuardBlock, false
	}
}

func extractAdGuardRule(rule string) (domain string, ruleKey string, hasBadfilter bool) {
	ruleBody := rule
	if idx := strings.IndexAny(ruleBody, "^$/|"); idx != -1 {
		ruleBody = ruleBody[:idx]
	}
	domain = normalizeDomain(ruleBody)
	if domain == "" {
		return "", "", false
	}

	modifierSection := ""
	if idx := strings.IndexRune(rule, '$'); idx != -1 {
		modifierSection = rule[idx+1:]
	}

	var normalizedModifiers []string
	if modifierSection != "" {
		for _, modifier := range strings.Split(modifierSection, ",") {
			modifier = normalizeDomain(modifier)
			if modifier == "" {
				continue
			}
			if modifier == "badfilter" {
				hasBadfilter = true
				continue
			}
			normalizedModifiers = append(normalizedModifiers, modifier)
		}
		sort.Strings(normalizedModifiers)
	}

	ruleKey = domain + "|" + strings.Join(normalizedModifiers, ",")
	return domain, ruleKey, hasBadfilter
}

func containsAny(line string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func isHostsMappingIP(field string) bool {
	switch field {
	case "0.0.0.0", "127.0.0.1", "::", "::1":
		return true
	default:
		return strings.Contains(field, ":")
	}
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
		switch len(fields) {
		case 1:
			line = fields[0]
		case 2:
			if isHostsMappingIP(fields[0]) {
				line = fields[1]
			} else {
				continue
			}
		default:
			continue
		}

		line = strings.ToLower(line)
		line = strings.TrimSuffix(line, ".")
		
		if line != "" && line != "0.0.0.0" && line != "127.0.0.1" {
			onDomain(line)
		}
	}
	return scanner.Err()
}
