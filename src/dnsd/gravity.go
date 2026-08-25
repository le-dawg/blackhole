package dnsd

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var DefaultLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

var legacyDefaultLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

const (
	gravityStateMetaKey       = "__blackhole_meta__"
	maxBlocklistResponseBytes = 64 * 1024 * 1024
)

type GravityState struct {
	ETag         string   `json:"etag"`
	LastModified string   `json:"last_modified"`
	Exceptions   []string `json:"exceptions,omitempty"`
	RuleCount    int      `json:"rule_count,omitempty"`
	SHA256       string   `json:"sha256,omitempty"`
}

func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type boundedReader struct {
	r     io.Reader
	limit int64
	read  int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += int64(n)
	if b.read > b.limit {
		return n, fmt.Errorf("feed size exceeded maximum limit of %d bytes", b.limit)
	}
	return n, err
}

type gravityPublishJournal struct {
	PreviousState map[string]GravityState `json:"previous_state"`
	NextState     map[string]GravityState `json:"next_state"`
	Caches        []gravityJournalCache   `json:"caches"`
}

type gravityJournalCache struct {
	CachePath        string `json:"cache_path"`
	HadPreviousCache bool   `json:"had_previous_cache"`
}

var createSiblingTempFileFunc = createSiblingTempFile
var saveStateMapFunc = saveStateMap
var renameFileFunc = os.Rename
var removeFileFunc = os.Remove
var syncFileFunc = func(f *os.File) error { return f.Sync() }
var syncDirFunc = func(dir string) error {
	df, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer df.Close()
	return df.Sync()
}
var statFileFunc = os.Stat

var (
	parserMu   sync.RWMutex
	parsersMap = make(map[string]any)
)

// RegisterParserForURL allows users to inject a custom parser implementation (ListParser or RuleAwareListParser) for a specific URL prefix.
func RegisterParserForURL(urlPrefix string, p any) {
	if p == nil {
		return
	}
	v := reflect.ValueOf(p)
	if (v.Kind() == reflect.Chan || v.Kind() == reflect.Func || v.Kind() == reflect.Map || v.Kind() == reflect.Pointer || v.Kind() == reflect.UnsafePointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Slice) && v.IsNil() {
		return
	}
	switch p.(type) {
	case ListParser, RuleAwareListParser:
		parserMu.Lock()
		parsersMap[urlPrefix] = p
		parserMu.Unlock()
	default:
		log.Printf("Warning: RegisterParserForURL ignored unsupported parser type %T for %s", p, urlPrefix)
	}
}

func init() {
	RegisterParserForURL("https://adguardteam.github.io/", &BlocklistParser{})
}

func loadStateMap(path string) map[string]GravityState {
	m := make(map[string]GravityState)
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Failed to open state map: %v", err)
		}
		return m
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	if err := dec.Decode(&m); err != nil && err != io.EOF {
		log.Printf("Failed to decode state map: %v", err)
		return make(map[string]GravityState)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		log.Printf("Failed to decode state map: trailing data")
		return make(map[string]GravityState)
	}
	if m == nil {
		return make(map[string]GravityState)
	}
	return m
}

func saveStateMap(path string, m map[string]GravityState) error {
	return writeJSONFile(path, m)
}

func writeJSONFile(path string, value any) error {
	f, tempPath, err := createSiblingTempFile(path)
	if err != nil {
		log.Printf("Failed to create temp state map file: %v", err)
		return err
	}

	if err := json.NewEncoder(f).Encode(value); err != nil {
		log.Printf("Failed to encode state map: %v", err)
		f.Close()
		_ = removeFileFunc(tempPath)
		return err
	}
	if err := syncFileFunc(f); err != nil {
		f.Close()
		_ = removeFileFunc(tempPath)
		return err
	}

	if err := f.Close(); err != nil {
		log.Printf("Failed to close temp state map file: %v", err)
		_ = removeFileFunc(tempPath)
		return err
	}

	if err := renameFileFunc(tempPath, path); err != nil {
		log.Printf("Failed to rename temp state map to final path: %v", err)
		_ = removeFileFunc(tempPath)
		return err
	}
	if err := syncDirFunc(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func createSiblingTempFile(path string) (*os.File, string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return nil, "", err
	}
	return f, f.Name(), nil
}

func cachePathForURL(dir, url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(dir, "gravity-"+hex.EncodeToString(sum[:8])+".cache")
}

func legacyCachePathForURL(dir, url string) (string, bool) {
	for index, legacyURL := range legacyDefaultLists {
		if legacyURL == url {
			return filepath.Join(dir, fmt.Sprintf("gravity-%d.cache", index)), true
		}
	}
	return "", false
}

func openCacheForSource(dir, url string, index int) (*os.File, error) {
	cachePath := cachePathForURL(dir, url)
	backupPath := cachePath + ".bak"
	if f, err := os.Open(cachePath); err == nil {
		return f, nil
	}
	if _, err := statFileFunc(backupPath); err == nil {
		if err := renameFileFunc(backupPath, cachePath); err != nil {
			return nil, err
		}
		return os.Open(cachePath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	legacyCachePath, ok := legacyCachePathForURL(dir, url)
	if !ok {
		return nil, os.ErrNotExist
	}
	if _, err := statFileFunc(legacyCachePath); err != nil {
		return nil, err
	}

	if err := renameFileFunc(legacyCachePath, cachePath); err == nil {
		return os.Open(cachePath)
	}

	return os.Open(legacyCachePath)
}

var builtinProductionLists = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://small.oisd.nl/domainswild",
	"https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
}

func loadCachedSource(dir, url string, index int, stateMap map[string]GravityState, readers *[]io.Reader, filesToClose *[]*os.File, gravityAllowlist map[string]bool) bool {
	f, err := openCacheForSource(dir, url, index)
	if err != nil {
		return false
	}
	cachedState, hasState := stateMap[url]
	if !hasState || cachedState.SHA256 == "" {
		_ = f.Close()
		return false
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		return false
	}
	gotHash := hex.EncodeToString(h.Sum(nil))
	if gotHash != cachedState.SHA256 {
		log.Printf("Security alert: cache digest mismatch for %s (expected %s, got %s)", url, cachedState.SHA256, gotHash)
		_ = f.Close()
		return false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return false
	}

	minRules := minRulesForURL(url)
	if cachedState.RuleCount > 10 {
		retainedFloor := (cachedState.RuleCount + 1) / 2
		if retainedFloor > minRules {
			minRules = retainedFloor
		}
	}
	valid, err := cacheHasEffectiveRules(f, minRules)
	if err != nil || !valid {
		_ = f.Close()
		return false
	}
	exceptions := sanitizedExceptions(cachedState.Exceptions)
	*readers = append(*readers, f)
	*filesToClose = append(*filesToClose, f)
	for _, domain := range exceptions {
		gravityAllowlist[domain] = true
	}
	return true
}

func minRulesForURL(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 1
	}
	host := strings.ToLower(u.Hostname())
	if host == "127.0.0.1" || host == "localhost" {
		return 1
	}
	for _, def := range builtinProductionLists {
		if rawURL == def {
			return 100
		}
	}
	return 2
}

func cacheHasEffectiveRules(f *os.File, minRequired int) (bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(f)
	unique := make(map[string]struct{})
	for scanner.Scan() {
		d := normalizeDomain(scanner.Text())
		if isEffectiveDomain(d) {
			unique[d] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if minRequired < 1 {
		minRequired = 1
	}
	return len(unique) >= minRequired, nil
}

func gravityPublishJournalPath(dir string) string {
	return filepath.Join(dir, "gravity.publish.json")
}

func validateJournalCaches(dir string, caches []gravityJournalCache) error {
	if len(caches) == 0 {
		return fmt.Errorf("invalid gravity publish journal")
	}
	seen := make(map[string]struct{}, len(caches))
	cleanDir := filepath.Clean(dir)
	cachePattern := regexp.MustCompile(`^gravity-[0-9a-f]{16}\.cache$`)
	for _, entry := range caches {
		if entry.CachePath == "" {
			return fmt.Errorf("invalid gravity publish journal")
		}
		cleanPath := filepath.Clean(entry.CachePath)
		if filepath.Dir(cleanPath) != cleanDir {
			return fmt.Errorf("invalid gravity publish journal")
		}
		if !cachePattern.MatchString(filepath.Base(cleanPath)) {
			return fmt.Errorf("invalid gravity publish journal")
		}
		if _, exists := seen[cleanPath]; exists {
			return fmt.Errorf("invalid gravity publish journal")
		}
		seen[cleanPath] = struct{}{}
	}
	return nil
}

func newGravityCommitMarker() string {
	return fmt.Sprintf("commit-%d", time.Now().UnixNano())
}

func saveGravityPublishJournal(dir string, previousState map[string]GravityState, nextState map[string]GravityState, caches []gravityJournalCache) error {
	journal := gravityPublishJournal{
		PreviousState: previousState,
		NextState:     nextState,
		Caches:        caches,
	}
	return writeJSONFile(gravityPublishJournalPath(dir), journal)
}

func recoverGravityArtifacts(dir string, statePath string) error {
	journalPath := gravityPublishJournalPath(dir)
	if _, err := statFileFunc(journalPath); err == nil {
		f, err := os.Open(journalPath)
		if err != nil {
			return err
		}
		var journal gravityPublishJournal
		dec := json.NewDecoder(f)
		decodeErr := dec.Decode(&journal)
		if decodeErr != nil {
			_ = f.Close()
			return decodeErr
		}
		var trailing json.RawMessage
		if err := dec.Decode(&trailing); err != io.EOF {
			_ = f.Close()
			return fmt.Errorf("invalid gravity publish journal")
		}
		_ = f.Close()
		if journal.PreviousState == nil || journal.NextState == nil || len(journal.Caches) == 0 {
			return fmt.Errorf("invalid gravity publish journal")
		}
		if _, ok := journal.NextState[gravityStateMetaKey]; !ok {
			return fmt.Errorf("invalid gravity publish journal")
		}
		if err := validateJournalCaches(dir, journal.Caches); err != nil {
			return err
		}
		currentState := loadStateMap(statePath)
		validNextCaches := true
		if reflect.DeepEqual(currentState, journal.NextState) {
			for url, st := range journal.NextState {
				if url == gravityStateMetaKey {
					continue
				}
				cPath := cachePathForURL(dir, url)
				if cf, err := os.Open(cPath); err == nil {
					h := sha256.New()
					_, _ = io.Copy(h, cf)
					_ = cf.Close()
					if st.SHA256 == "" || hex.EncodeToString(h.Sum(nil)) != st.SHA256 {
						validNextCaches = false
						break
					}
				} else {
					validNextCaches = false
					break
				}
			}
		} else {
			validNextCaches = false
		}

		if validNextCaches {
			for _, entry := range journal.Caches {
				if err := removeFileFunc(entry.CachePath + ".bak"); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		} else {
			for _, entry := range journal.Caches {
				backupPath := entry.CachePath + ".bak"
				if _, err := statFileFunc(backupPath); err == nil {
					if err := removeFileFunc(entry.CachePath); err != nil && !os.IsNotExist(err) {
						return err
					}
					if err := renameFileFunc(backupPath, entry.CachePath); err != nil {
						return err
					}
					continue
				} else if !os.IsNotExist(err) {
					return err
				}
				if !entry.HadPreviousCache {
					if err := removeFileFunc(entry.CachePath); err != nil && !os.IsNotExist(err) {
						return err
					}
				}
			}
			if journal.PreviousState == nil {
				journal.PreviousState = make(map[string]GravityState)
			}
			if err := saveStateMap(statePath, journal.PreviousState); err != nil {
				return err
			}
		}
		if err := removeFileFunc(journalPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tempPatterns := []string{
		filepath.Join(dir, "gravity-*.cache.tmp-*"),
		filepath.Join(dir, "gravity.state.json.tmp-*"),
		filepath.Join(dir, "gravity.publish.json.tmp-*"),
	}
	for _, pattern := range tempPatterns {
		tempPaths, _ := filepath.Glob(pattern)
		for _, tempPath := range tempPaths {
			_ = removeFileFunc(tempPath)
		}
	}
	return nil
}

func StartGravitySync(ctx context.Context, dir string, r *FilterEngine) error {
	if err := refreshGravity(ctx, dir, r); err != nil {
		return fmt.Errorf("failed initial gravity refresh: %w", err)
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := refreshGravity(ctx, dir, r); err != nil {
					log.Printf("Gravity background update error: %v", err)
				}
			}
		}
	}()
	return nil
}

func refreshGravity(ctx context.Context, dir string, r *FilterEngine) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create gravity data dir: %w", err)
	}
	if len(DefaultLists) == 0 {
		return fmt.Errorf("no gravity sources configured")
	}

	statePath := filepath.Join(dir, "gravity.state.json")
	if err := recoverGravityArtifacts(dir, statePath); err != nil {
		return fmt.Errorf("recover gravity artifacts: %w", err)
	}
	stateMap := loadStateMap(statePath)

	client := &http.Client{Timeout: 30 * time.Second}

	var readers []io.Reader
	var filesToClose []*os.File
	gravityAllowlist := make(map[string]bool)
	type stagedSource struct {
		url       string
		cachePath string
		tempPath  string
		state     GravityState
	}
	var stagedSources []stagedSource

	defer func() {
		for _, f := range filesToClose {
			f.Close()
		}
		for _, staged := range stagedSources {
			if staged.tempPath != "" {
				_ = removeFileFunc(staged.tempPath)
			}
		}
	}()

	for i, url := range DefaultLists {
		cachePath := cachePathForURL(dir, url)
		sourceExceptions := make(map[string]bool)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			log.Printf("Failed to create request for %s: %v", url, err)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}
		if state, ok := stateMap[url]; ok {
			if state.ETag != "" {
				req.Header.Set("If-None-Match", state.ETag)
			}
			if state.LastModified != "" {
				req.Header.Set("If-Modified-Since", state.LastModified)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to fetch %s: %v", url, err)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			log.Printf("Failed to fetch %s, status code: %d", url, resp.StatusCode)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		if resp.ContentLength > maxBlocklistResponseBytes {
			resp.Body.Close()
			log.Printf("Failed to fetch %s: Content-Length %d exceeds max allowed size %d", url, resp.ContentLength, maxBlocklistResponseBytes)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		f, tempCache, err := createSiblingTempFileFunc(cachePath)
		if err != nil {
			log.Printf("Failed to create temp cache %s: %v", tempCache, err)
			resp.Body.Close()
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		writer := bufio.NewWriter(f)
		parserMu.RLock()
		var parser any = &PiHoleParser{}

		var prefixes []string
		for prefix := range parsersMap {
			prefixes = append(prefixes, prefix)
		}

		// Sort prefixes by length descending to match the longest prefix
		sort.Slice(prefixes, func(i, j int) bool {
			return len(prefixes[i]) > len(prefixes[j])
		})

		for _, prefix := range prefixes {
			if strings.HasPrefix(url, prefix) {
				parser = parsersMap[prefix]
				break
			}
		}
		parserMu.RUnlock()

		var writeErr error
		var parseErr error
		blockCount := 0
		uniqueBlockDomains := make(map[string]struct{})
		exceptionCount := 0
		limitedBody := &boundedReader{r: resp.Body, limit: maxBlocklistResponseBytes}
		if ruleAwareParser, ok := parser.(RuleAwareListParser); ok {
			parseErr = ruleAwareParser.ParseRules(
				limitedBody,
				func(domain string) {
					domain = normalizeDomain(domain)
					if !isEffectiveDomain(domain) {
						return
					}
					blockCount++
					uniqueBlockDomains[domain] = struct{}{}
					if writeErr == nil {
						if _, err := writer.WriteString(domain + "\n"); err != nil {
							writeErr = err
						}
					}
				},
				func(domain string) {
					domain = normalizeDomain(domain)
					if !isEffectiveDomain(domain) {
						return
					}
					exceptionCount++
					sourceExceptions[domain] = true
				},
			)
		} else if standardParser, ok := parser.(ListParser); ok {
			parseErr = standardParser.Parse(limitedBody, func(domain string) {
				domain = normalizeDomain(domain)
				if !isEffectiveDomain(domain) {
					return
				}
				blockCount++
				uniqueBlockDomains[domain] = struct{}{}
				if writeErr == nil {
					if _, err := writer.WriteString(domain + "\n"); err != nil {
						writeErr = err
					}
				}
			})
		} else {
			parseErr = fmt.Errorf("unsupported parser type for %s", url)
		}

		// Drain unconsumed bytes from limitedBody to ensure total response does not exceed limit
		if _, err := io.Copy(io.Discard, limitedBody); err != nil && parseErr == nil {
			parseErr = err
		}

		if writeErr == nil && parseErr != nil {
			writeErr = parseErr
		}
		if writeErr == nil && blockCount == 0 {
			writeErr = fmt.Errorf("source produced no effective block rules")
		}
		minRequired := minRulesForURL(url)
		if writeErr == nil && len(uniqueBlockDomains) < minRequired {
			writeErr = fmt.Errorf("source produced insufficient effective block rules (%d < %d)", len(uniqueBlockDomains), minRequired)
		}
		if writeErr == nil && stateMap[url].RuleCount > 10 && len(uniqueBlockDomains) < (stateMap[url].RuleCount+1)/2 {
			writeErr = fmt.Errorf("source suffered an unexpected rule drop from %d to %d rules", stateMap[url].RuleCount, len(uniqueBlockDomains))
		}

		if writeErr == nil {
			if err := writer.Flush(); err != nil {
				writeErr = err
			}
		}
		if writeErr == nil {
			if err := syncFileFunc(f); err != nil {
				writeErr = err
			}
		}

		if err := f.Close(); err != nil && writeErr == nil {
			writeErr = err
		}

		resp.Body.Close()

		var cacheHash string
		if writeErr == nil {
			cacheHash, writeErr = computeFileSHA256(tempCache)
		}

		if writeErr != nil {
			log.Printf("Error processing blocklist %s: %v", url, writeErr)
			_ = removeFileFunc(tempCache)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}

		fRead, err := os.Open(tempCache)
		if err != nil {
			log.Printf("Failed to open staged cache for %s: %v", url, err)
			_ = removeFileFunc(tempCache)
			loadCachedSource(dir, url, i, stateMap, &readers, &filesToClose, gravityAllowlist)
			continue
		}
		readers = append(readers, fRead)
		filesToClose = append(filesToClose, fRead)
		stagedSources = append(stagedSources, stagedSource{
			url:       url,
			cachePath: cachePath,
			tempPath:  tempCache,
			state: GravityState{
				ETag:         resp.Header.Get("ETag"),
				LastModified: resp.Header.Get("Last-Modified"),
				Exceptions:   sortedKeys(sourceExceptions),
				RuleCount:    len(uniqueBlockDomains),
				SHA256:       cacheHash,
			},
		})
		for domain := range sourceExceptions {
			gravityAllowlist[domain] = true
		}
	}

	if len(readers) < len(DefaultLists) {
		return fmt.Errorf("incomplete gravity sources: %d of %d available", len(readers), len(DefaultLists))
	}

	multiReader := io.MultiReader(readers...)
	scanner := bufio.NewScanner(multiReader)
	newRoot := BuildTrieFromScanner(scanner)
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed scanning gravity lists: %w", err)
	}

	newStateMap := make(map[string]GravityState, len(stateMap)+len(stagedSources))
	for url, state := range stateMap {
		if url == gravityStateMetaKey {
			continue
		}
		newStateMap[url] = state
	}
	for _, staged := range stagedSources {
		newStateMap[staged.url] = staged.state
	}

	// Stale source eviction: clean up caches and state for sources removed from DefaultLists
	activeURLs := make(map[string]bool)
	for _, u := range DefaultLists {
		activeURLs[u] = true
	}
	for u := range newStateMap {
		if !activeURLs[u] {
			oldCache := cachePathForURL(dir, u)
			_ = removeFileFunc(oldCache)
			_ = removeFileFunc(oldCache + ".bak")
			delete(newStateMap, u)
		}
	}

	newStateMap[gravityStateMetaKey] = GravityState{ETag: newGravityCommitMarker()}
	type publishedCache struct {
		cachePath  string
		backupPath string
	}
	var published []publishedCache
	journalCaches := make([]gravityJournalCache, 0, len(stagedSources))
	rollbackPublishedCaches := func() {
		for i := len(published) - 1; i >= 0; i-- {
			entry := published[i]
			_ = removeFileFunc(entry.cachePath)
			if _, statErr := statFileFunc(entry.backupPath); statErr == nil {
				_ = renameFileFunc(entry.backupPath, entry.cachePath)
			}
		}
	}
	for _, staged := range stagedSources {
		_, statErr := statFileFunc(staged.cachePath)
		if statErr != nil && !os.IsNotExist(statErr) {
			return fmt.Errorf("stat cache for %s: %w", staged.url, statErr)
		}
		journalCaches = append(journalCaches, gravityJournalCache{
			CachePath:        staged.cachePath,
			HadPreviousCache: statErr == nil,
		})
	}
	if len(stagedSources) > 0 {
		if err := saveGravityPublishJournal(dir, stateMap, newStateMap, journalCaches); err != nil {
			return fmt.Errorf("save gravity publish journal: %w", err)
		}
	}
	for idx := range stagedSources {
		staged := &stagedSources[idx]
		backupPath := staged.cachePath + ".bak"
		if err := removeFileFunc(backupPath); err != nil && !os.IsNotExist(err) {
			rollbackPublishedCaches()
			return fmt.Errorf("prepare backup for %s: %w", staged.url, err)
		}
		if err := renameFileFunc(staged.cachePath, backupPath); err != nil && !os.IsNotExist(err) {
			rollbackPublishedCaches()
			return fmt.Errorf("backup existing cache for %s: %w", staged.url, err)
		}
		if err := renameFileFunc(staged.tempPath, staged.cachePath); err != nil {
			if _, statErr := statFileFunc(backupPath); statErr == nil {
				_ = renameFileFunc(backupPath, staged.cachePath)
			}
			rollbackPublishedCaches()
			return fmt.Errorf("commit staged cache for %s: %w", staged.url, err)
		}
		staged.tempPath = ""
		published = append(published, publishedCache{cachePath: staged.cachePath, backupPath: backupPath})
	}
	if len(stagedSources) > 0 {
		if err := syncDirFunc(dir); err != nil {
			rollbackPublishedCaches()
			return fmt.Errorf("sync published caches dir: %w", err)
		}
	}

	if err := saveStateMapFunc(statePath, newStateMap); err != nil {
		currentState := loadStateMap(statePath)
		if reflect.DeepEqual(currentState, newStateMap) {
			r.UpdateGravityData(newRoot, gravityAllowlist)
			return fmt.Errorf("save gravity state: %w", err)
		}
		rollbackPublishedCaches()
		if restoreErr := saveStateMap(statePath, stateMap); restoreErr != nil {
			return fmt.Errorf("save gravity state: %w; restore state: %v", err, restoreErr)
		}
		return fmt.Errorf("save gravity state: %w", err)
	}
	r.UpdateGravityData(newRoot, gravityAllowlist)
	for _, entry := range published {
		if err := removeFileFunc(entry.backupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cleanup published backup: %w", err)
		}
	}
	if len(stagedSources) > 0 {
		if err := removeFileFunc(gravityPublishJournalPath(dir)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove gravity publish journal: %w", err)
		}
	}
	if len(stagedSources) > 0 {
		if err := syncDirFunc(dir); err != nil {
			return fmt.Errorf("sync post-publish dir: %w", err)
		}
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sanitizedExceptions(exceptions []string) []string {
	if len(exceptions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(exceptions))
	sanitized := make([]string, 0, len(exceptions))
	for _, domain := range exceptions {
		domain = normalizeDomain(domain)
		if !isEffectiveDomain(domain) {
			continue
		}
		if _, exists := seen[domain]; exists {
			continue
		}
		seen[domain] = struct{}{}
		sanitized = append(sanitized, domain)
	}
	return sanitized
}

func isEffectiveDomain(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	if !strings.Contains(domain, ".") {
		return false
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}
