# Final OSS Polish: The v1.0 Eradication Plan

**Objective:** Weed out the 5 critical weaknesses identified in the final Codex audit with extreme prejudice. Prepare the repository for top-shelf open-source publication.
**Methodology:** Multiagent Execution Plan.

## Phase 1: Repo Eradication & Documentation (Agent: The Janitor)
**Goal:** Remove all hacking residue and establish a professional OSS presence.
- [ ] **Task 1.1:** Execute a ruthless `rm` on all `.orig`, `.rej`, `.diff`, and rogue `.py` patch scripts in the repository root and subdirectories.
- [ ] **Task 1.2:** Remove checked-in binaries from the root.
- [ ] **Task 1.3:** Draft a CNCF-standard `README.md` encompassing the project vision, Go/Swift architecture, build instructions (`make build`), and installation process.

## Phase 2: Architectural Purification (Parallel Agents)

### Agent 2A: The Swift Purist
**Goal:** Achieve true MVVM decoupling.
- [ ] **Task 2A.1:** Audit `PopoverView.swift` and `BlackholeApp.swift`. Identify any direct invocations of `UnixSocketTransport`.
- [ ] **Task 2A.2:** Reroute all network and state side-effects strictly through `AppViewModel` intents. The Views must be completely dumb, only reflecting `@Observable` state.
- [ ] **Task 2A.3:** Remove all "refactor residue" comments acknowledging mid-refactor state.

### Agent 2B: The Go Test Engineer
**Goal:** Eradicate non-hermetic Go tests.
- [ ] **Task 2B.1:** Refactor `src/dnsd/ipc_server_test.go` and `forwarder_test.go`.
- [ ] **Task 2B.2:** Inject mock listeners/connections instead of binding to real machine ports, ensuring 100% test reliability in sandboxed CI environments.

## Phase 3: The Swift Testing Program (Agent: iOS/macOS SDET)
**Goal:** Real coverage, no tautologies.
- [ ] **Task 3.1:** Delete the `#expect(true == true)` camouflage test.
- [ ] **Task 3.2:** Write a mock `UnixSocketTransport` interface to allow testing without the Go daemon.
- [ ] **Task 3.3:** Write behavior-driven tests for `AppViewModel` (e.g., verifying that toggling `isDnsActive` triggers the correct payload encoding).

## Phase 4: Release Pipeline Forging (Agent: Release Engineer)
**Goal:** A legitimate, shippable macOS release bundle.
- [ ] **Task 4.1:** Generate a valid `Info.plist` with standard macOS bundle identifiers.
- [ ] **Task 4.2:** Update the `Makefile` `package` target to assemble a true `.app` directory structure (`Contents/MacOS`, `Contents/Resources`, `Contents/Info.plist`).
- [ ] **Task 4.3:** Automate SHA256 checksum generation during `make package` and inject it dynamically into `Casks/blackhole.rb` using a templating script.

## Verification Gate (Manual / Main Agent)
- [ ] Run `go test -v ./...` (Verify hermetic Go tests).
- [ ] Run `cd MenuBar && swift test` (Verify Swift mocks).
- [ ] Run `make package` (Verify valid app bundle structure and SHA injection).
