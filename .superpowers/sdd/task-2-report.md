# Task 2: Blocklist Engine - Hot-Reload Blocklist & Whitelist

## Implementation Details
I added a `whitelist` and `blacklist` (both `map[string]bool`) to the `Resolver` struct in `src/dnsd/resolver.go`. I added a thread-safe `SetLists` method to the `Resolver` that takes these maps and updates the resolver state. I modified the `Resolve` function to immediately return based on whitelist and blacklist checks before consulting the Trie for domains.
Then, I created `src/dnsd/userlist.go` which uses `github.com/fsnotify/fsnotify` to track changes to `whitelist.txt` and `blacklist.txt` inside a specific directory. Upon detecting file modifications, it re-reads the files and updates the resolver maps via `SetLists`.

## Deviations
- The brief asked to modify `IsBlocked` in `resolver.go`. In the actual codebase, the method was named `Resolve`. I used `Resolve`.
- The brief used `NewResolver()` without arguments in the tests. The actual `NewResolver` requires an `upstreams []string` parameter, so I passed `nil` in the test setup.
- The brief didn't include `mu sync.RWMutex` initialization correctly if copying verbatim, so I matched the existing `Resolver` definition in `src/dnsd/resolver.go`.

## Test Results
Ran `go test -run TestUserListWatcher -v` in `src/dnsd`. The test successfully validated initial loading of the lists, proper behavior (whitelist overrides the domain block), and successful hot-reloading after files were modified, passing in `~0.20s`.

## Self-Review
- Confirmed that fsnotify adds the directory rather than just the files, because files might be removed and replaced by text editors.
- Ensured thread safety for `whitelist` and `blacklist` maps by leveraging the existing `sync.RWMutex` on the `Resolver`.
- Modified tests to use the actual signature of `NewResolver` and `Resolve` so that the code compiles and tests reliably pass.
