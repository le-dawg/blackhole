# Spec A — Blocklist Engine

## Goal
Replace the two hardcoded blocked domains in `src/main.go` with a full production-grade
blocklist ingestion pipeline that downloads, parses, caches, and hot-reloads 100k+ ad/tracker
domains with sub-millisecond Trie lookup and zero daemon restarts on change.

## Scope
Items from Codex audit: 1.1, 1.2, 1.3, 1.4, 3.2, 3.3

## Global Constraints
- Platform: macOS 15+
- Daemon RAM budget: < 15 MB total (Trie + cache combined)
- Zero restarts on list update or exclusion change
- All file paths relative to `~/Library/Application Support/blackhole/`

---

## Components

### 1. Multi-Format Blocklist Parser (`src/dnsd/blocklist_parser.go`)
Parses three input formats into a normalized `[]string` of lowercase domains:

| Format | Example line | Rule |
|--------|-------------|------|
| Hosts file | `0.0.0.0 ads.example.com` | Split on whitespace, take field[1]; skip if field[0] is not `0.0.0.0` or `127.0.0.1` |
| Plain domain | `ads.example.com` | Use as-is after trimming whitespace |
| ABP/AdGuard | `\|\|ads.example.com^` | Strip leading `\|\|` and trailing `^` |

Lines beginning with `#` or `!` are comments and are skipped. All output domains are lowercased
and have trailing dots stripped. Input files up to 20 MB must parse in < 200 ms.

### 2. Gravity Downloader & Cache (`src/dnsd/gravity.go`)
Manages downloading and on-disk caching of remote blocklists.

**Default bundled list URLs** (embedded as Go constants):
```
https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
https://small.oisd.nl/domainswild
https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt
```

**Behavior:**
- On first launch (no cache file present): download all lists, parse, merge, deduplicate, write
  merged domain list to `~/Library/Application Support/blackhole/gravity.cache`.
- On subsequent launches: load from `gravity.cache` if it exists and is < 25 hours old.
- Background refresh goroutine runs every 24 hours using HTTP conditional requests
  (`If-None-Match` with stored ETag, `If-Modified-Since` with stored timestamp).
- If all downloads fail (offline): log a warning and continue using the last valid cache.
  Never crash the daemon due to a failed list refresh.
- After a successful refresh the Trie is rebuilt atomically under a `sync.RWMutex` write lock
  so in-flight queries see either the old or the new Trie, never a partial state.

### 3. Custom User Lists (`src/dnsd/userlist.go`)
Reads two optional plain-text files from the user's Application Support directory:

- `whitelist.txt` — one domain per line; these domains are **never** blocked regardless of
  gravity list membership. Whitelist takes highest precedence.
- `blacklist.txt` — one domain per line; these domains are **always** blocked regardless of
  whitelist or process exclusion.

**Hot-reload:** Uses `github.com/fsnotify/fsnotify` to watch both files. On `WRITE` or
`CREATE` events, re-reads the file and rebuilds the whitelist/blacklist sets under a
`sync.RWMutex`. No daemon restart required.

### 4. Configurable Process CLI Argument Matching (`src/dnsd/process_monitor.go` patch)
The existing C helper (`vpn_monitor.c`) currently hardcodes the substring `litellm` for
CLI argument matching. This is made configurable by:

- Reading a `cli_patterns` array from `exclusions.json` alongside existing `BundleID` entries.
- Passing the pattern list into the C shim at startup via a Go string array.
- The C helper iterates the pattern list instead of a single hardcoded string.

Example `exclusions.json` entry:
```json
{ "name": "LiteLLM", "cliPattern": "litellm", "isExcluded": true }
```

### 5. Exclusions File Watcher (`src/dnsd/exclusions.go` patch)
Replace the current per-query `LoadExclusions` disk read with a single `fsnotify` watcher
on `exclusions.json`. The watcher goroutine reloads and rebuilds the exclusions list on any
file change and stores it in a `sync.RWMutex`-protected struct. Query handlers read the
cached struct, eliminating per-query disk I/O.

---

## Data Flow

```
[Remote URLs] → gravity.go (HTTP download + ETag cache) → blocklist_parser.go
                                                                   ↓
[whitelist.txt] → userlist.go ──────────────────────────→ Trie merge (RWMutex write)
[blacklist.txt] → userlist.go ──────────────────────────→       ↓
                                                          Resolver.Trie (read path)
[exclusions.json] → fsnotify watcher → ExclusionList (RWMutex)
                                              ↓
                                    IsProcessExcluded()
```

---

## Testing Requirements
- Unit test: parser correctly normalizes all three input formats
- Unit test: parser skips comment lines and malformed entries
- Unit test: whitelist entry prevents block even when domain is in gravity list
- Unit test: blacklist entry blocks domain even when process is in exclusions
- Unit test: fsnotify watcher reloads exclusions without restart (use temp file)
- Integration test: gravity downloader falls back to cache when HTTP returns 304 (Not Modified)
- Benchmark: Trie lookup < 1 µs for 100k domain list

---

## Files Created / Modified
| Action | Path |
|--------|------|
| Create | `src/dnsd/blocklist_parser.go` |
| Create | `src/dnsd/blocklist_parser_test.go` |
| Create | `src/dnsd/gravity.go` |
| Create | `src/dnsd/gravity_test.go` |
| Create | `src/dnsd/userlist.go` |
| Create | `src/dnsd/userlist_test.go` |
| Modify | `src/dnsd/exclusions.go` (add fsnotify watcher, remove per-query disk read) |
| Modify | `src/dnsd/process_monitor.go` (make CLI patterns configurable) |
| Modify | `src/main.go` (remove hardcoded `AddBlockedDomain` calls; wire gravity loader) |
