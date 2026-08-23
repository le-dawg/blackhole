# Release Maturity Audit: `blackhole`

## Verdict

**Not ready for a v1.0 / “Top Shelf OSS” release.**

The repository is clearly better than the pre-remediation state: the Go daemon is less monolithic, the fake lifetime metrics are gone, the Swift app no longer shells out to `curl`, and release packaging has been converged around `blackhole-release.zip`. But the project still has **multiple P1 blockers** across security, release engineering, and verification maturity. I do **not** see a remaining P0-class catastrophic flaw on the evidence available here, but I do see enough unresolved risk that publishing this as a polished CNCF-grade OSS release would be premature.

## Findings

### P1: IPC socket is still globally writable/readable and lacks any trust boundary
The socket was moved out of `/tmp`, which is good, but the effective security model is still too weak for a privileged local daemon. The daemon explicitly sets the socket mode to `0666`, meaning any local user/process can connect, read query telemetry, and trigger pause/resume. There is also no peer credential validation, no caller authorization, and no method restriction on the handlers. This is an improvement in placement, not a complete security fix.  
Refs: [src/dnsd/ipc_server.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server.go:25), [src/dnsd/ipc_server.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server.go:33), [src/dnsd/ipc_server.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server.go:40), [src/dnsd/ipc_server.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server.go:47), [src/dnsd/ipc_server.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server.go:58)

### P1: Release artifact is not a proper macOS app bundle
The packaging flow creates `Blackhole.app/Contents/MacOS/Blackhole`, but I found no bundled `Contents/Info.plist`, no bundle metadata, and no evidence of a real app bundle assembly. For a public macOS release, especially one distributed via Homebrew Cask, this is not release-grade. The docs/specs even expected an `Info.plist`, but the implementation does not produce one.  
Refs: [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:10), [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:12), [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:14), [MenuBar/Package.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Package.swift:12)

### P1: Homebrew Cask is not shippable
The cask still contains `sha256 "REPLACE_WITH_SHA256"`, which alone blocks a serious release. It also installs a binary into `/usr/local/bin` and then requires a manual privileged installer step, which is acceptable only if documented as a transitional path, not as finished release engineering.  
Refs: [Casks/blackhole.rb](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Casks/blackhole.rb:3), [Casks/blackhole.rb](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Casks/blackhole.rb:11), [Casks/blackhole.rb](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Casks/blackhole.rb:22)

### P1: Swift architecture is only partially cleaned up; MVVM claim is not actually true yet
The `curl` shell-outs are gone, which is real progress. But the view still calls the transport layer directly instead of routing through the view model, and DNS state changes still trigger side effects through `didSet`. The view model itself contains comments admitting the architecture is not actually settled. That is not “clean MVVM”; it is mid-refactor.  
Refs: [MenuBar/ViewModels/AppViewModel.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/ViewModels/AppViewModel.swift:7), [MenuBar/ViewModels/AppViewModel.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/ViewModels/AppViewModel.swift:15), [MenuBar/ViewModels/AppViewModel.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/ViewModels/AppViewModel.swift:61), [MenuBar/Views/PopoverView.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Views/PopoverView.swift:37), [MenuBar/Views/PopoverView.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Views/PopoverView.swift:42), [MenuBar/Views/PopoverView.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Views/PopoverView.swift:47), [MenuBar/Views/PopoverView.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Views/PopoverView.swift:59)

### P1: Test maturity is not at release standard, especially on the Swift side
The Swift test suite is effectively empty: one tautological `true == true` test. The new Unix socket transport, IPC contract, pause/resume behavior, and view-model behavior are not meaningfully covered. On the Go side, there are tests, but many are integration-heavy and rely on real sockets/ports rather than clean seams, which weakens portability and CI reliability.  
Refs: [MenuBar/Tests/MenuBarTests.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Tests/MenuBarTests.swift:4), [MenuBar/Tests/MenuBarTests.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Tests/MenuBarTests.swift:6), [src/dnsd/ipc_server_test.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/ipc_server_test.go:12), [src/dnsd/forwarder_test.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/forwarder_test.go:11), [src/dnsd/gravity_test.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/gravity_test.go:11)

### P1: OSS release hygiene is incomplete
There is no root `README.md` in the repo, despite plans/specs referring to one. I also found obvious repo debris and generated leftovers that should not survive into a polished public release: `patch_ipc.py`, `patch_main.py`, `patch.diff`, `src/main.go.orig`, `src/main.go.rej`, and checked-in binaries in the root. That is not fatal to functionality, but it is below publication standard.  
Refs: [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:1)

## What Was Actually Resolved

The remediation sweep did produce real gains:

- `main.go` was successfully decomposed; daemon/config concerns are clearer now.  
Refs: [src/main.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/main.go:1), [src/dnsd/daemon.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/daemon.go:16), [src/dnsd/config.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/config.go:8)

- The stats engine is no longer fake lifetime accumulation; it now maintains hourly buckets and prunes old data.  
Refs: [src/dnsd/stats.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/stats.go:32), [src/dnsd/stats.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/stats.go:43), [src/dnsd/stats.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/stats.go:67), [src/dnsd/stats.go](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/src/dnsd/stats.go:100)

- The Swift app no longer shells out to `curl`; it now uses a native Unix socket transport.  
Refs: [MenuBar/Services/UnixSocketTransport.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Services/UnixSocketTransport.swift:10), [MenuBar/Services/IPCClient.swift](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/MenuBar/Services/IPCClient.swift:37)

- Packaging was at least converged onto a single release zip target.  
Refs: [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:10), [Makefile](/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/Makefile:17)

## Category Assessment

**Code quality:** improving, but uneven. The Go daemon is materially better. The Swift app still has “refactor residue” and architectural leakage.

**Security posture:** improved from the `/tmp` socket era, but not yet strong enough. The remaining IPC authorization gap is the biggest issue.

**Release engineering:** not v1.0-grade. The artifact contract is cleaner, but the macOS bundle/cask/install story is still incomplete.

**Testing:** insufficient for a public 1.0. Go coverage exists; Swift coverage is largely absent; CI-grade determinism is not demonstrated.

## Release Decision

**Recommendation: do not cut v1.0 yet.**

### Remaining blockers before release
1. Lock down IPC access properly.
2. Produce a real macOS app bundle with metadata and validate install/uninstall end to end.
3. Finish the Swift architectural cleanup so the view model is the only mutation boundary.
4. Replace placeholder/empty tests with real contract tests for IPC, transport, pause/resume, and UI state transitions.
5. Add baseline OSS release docs and remove repo debris.

## Verification Notes

I validated the repository structure and reviewed the load-bearing source paths directly. I also attempted the repo’s verification commands:

- `make test`
- `go test -race ./src/dnsd/...`
- `cd MenuBar && swift test`

In this sandbox, Go socket/port-binding tests are blocked by environment permissions, and Swift testing is also blocked by local toolchain/SDK mismatch plus cache permission issues. That means I cannot certify runtime behavior from this environment alone. Even so, the blockers above are visible from source and release metadata, not just from failed local execution.

<!-- buddy: *bares teeth* moving the socket helped, but a world-writable control plane is still soft underbelly -->