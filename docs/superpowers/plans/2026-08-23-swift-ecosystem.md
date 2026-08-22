# Swift Ecosystem Upgrade Plan

**Goal**: Modernize the SwiftUI MenuBar app utilizing Swift Testing, Preview-Driven Development, and Swift 6 concurrency models.

- [ ] **Task 1: Shift Unit Tests from Classes to Structs**
  Convert legacy `XCTestCase` classes into Swift `@Suite` structs. Use standard struct `init()` and `deinit`. Replace `XCTAssert` with `#expect` and `#require`.
- [ ] **Task 2: Design IPC-Friendly ModelContexts**
  Define lightweight `ModelActor` wrappers for local data to ensure background tasks and IPC events can safely read/write to the `ModelContext` without triggering Swift 6 data-race errors.
- [ ] **Task 3: Enforce Preview-Driven UI**
  Decouple dropdown view bodies from raw SwiftData models. Create a `#Preview` trait that injects an in-memory-only ModelContainer to test the UI safely.
- [ ] **Task 4: CI & Automation Integration**
  Setup `.cursorrules` pointing to `.superpowers/skills/swift/`. Ensure the Xcode build system is configured to run the parallelized Swift Testing suites.
