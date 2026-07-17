# VPN Monitor Quality Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement quality improvements for the VPN Monitor: fix Cgo preamble definitions, resolve CFRunLoopSourceRef leak, resolve race conditions and synchronize start/stop via a channel, optimize SCDynamicStore reuse, and protect Start/Stop internal logic with a Go mutex lock.

**Architecture:** 
- Separate C definitions from Cgo preamble into `src/dnsd/vpn_monitor.c` and declare them as extern prototypes in `src/dnsd/vpn_monitor.go`.
- In `start_monitoring`, keep the `rls` run loop source in scope, call `goMonitorStarted()` before running, and clean it up after `CFRunLoopRun()` finishes.
- Use a channel in Go to synchronize run loop initialization. Define and export `goMonitorStarted` in Go to signal this channel.
- Reuse SCDynamicStore connection by passing `store` directly to `getDNSServers(store)` from the callback.
- Protect Go's `StartVPNMonitor` and `StopVPNMonitor` with a sync.Mutex.

**Tech Stack:** Go, Cgo, CoreFoundation, SystemConfiguration.

## Global Constraints

- Do not use any third-party library for the VPN monitor modifications.
- Maintain existing codebase patterns (errors, structures, concurrency models).
- All tests must compile and pass successfully.

---

### Task 1: Create vpn_monitor.c and Move C Implementations

**Files:**
- Create: `src/dnsd/vpn_monitor.c`
- Modify: `src/dnsd/vpn_monitor.go`

**Interfaces:**
- Consumes: C-level SystemConfiguration/CoreFoundation APIs.
- Produces: `vpn_monitor.c` with implementations and updated `vpn_monitor.go` preamble with declarations only.

- [ ] **Step 1: Write `src/dnsd/vpn_monitor.c`**
  Write the full implementation of C monitoring functions into the new C file, including prototype declarations for exported Go functions `goDNSCallback` and `goMonitorStarted`.
  Solve the resource leak by keeping `CFRunLoopSourceRef rls` in scope, adding it, calling `goMonitorStarted()`, running the run loop, and removing/releasing it after the loop returns.

  ```c
  #include <CoreFoundation/CoreFoundation.h>
  #include <SystemConfiguration/SystemConfiguration.h>
  #include <stdlib.h>

  // Forward declarations of exported Go functions
  void goDNSCallback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info);
  void goMonitorStarted(void);

  static void my_callback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info) {
  	goDNSCallback(store, changedKeys, info);
  }

  static CFRunLoopRef g_runLoop = NULL;

  int start_monitoring(const char* name) {
  	CFStringRef nameStr = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingUTF8);
  	if (!nameStr) {
  		goMonitorStarted();
  		return -1;
  	}

  	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
  	SCDynamicStoreRef store = SCDynamicStoreCreate(kCFAllocatorDefault, nameStr, my_callback, &context);
  	CFRelease(nameStr);
  	if (!store) {
  		goMonitorStarted();
  		return -1;
  	}

  	CFStringRef pattern = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
  	if (!pattern) {
  		CFRelease(store);
  		goMonitorStarted();
  		return -1;
  	}

  	CFArrayRef keys = CFArrayCreate(kCFAllocatorDefault, (const void **)&pattern, 1, &kCFTypeArrayCallBacks);
  	CFRelease(pattern);
  	if (!keys) {
  		CFRelease(store);
  		goMonitorStarted();
  		return -1;
  	}

  	SCDynamicStoreSetNotificationKeys(store, keys, NULL);
  	CFRelease(keys);

  	CFRunLoopSourceRef rls = SCDynamicStoreCreateRunLoopSource(kCFAllocatorDefault, store, 0);
  	CFRelease(store);
  	if (!rls) {
  		goMonitorStarted();
  		return -1;
  	}

  	CFRunLoopRef rl = CFRunLoopGetCurrent();
  	g_runLoop = rl;
  	CFRunLoopAddSource(rl, rls, kCFRunLoopCommonModes);

  	// Notify Go that g_runLoop is initialized and source is added
  	goMonitorStarted();

  	CFRunLoopRun();

  	// Clean up after CFRunLoopRun has returned
  	CFRunLoopRemoveSource(rl, rls, kCFRunLoopCommonModes);
  	CFRelease(rls);

  	return 0;
  }

  void stop_monitoring(void) {
  	if (g_runLoop) {
  		CFRunLoopStop(g_runLoop);
  		g_runLoop = NULL;
  	}
  }

  CFArrayRef copy_dns_servers(SCDynamicStoreRef store) {
  	if (!store) return NULL;
  	CFStringRef key = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
  	if (!key) return NULL;

  	CFPropertyListRef dict = SCDynamicStoreCopyValue(store, key);
  	CFRelease(key);
  	if (!dict) return NULL;

  	if (CFGetTypeID(dict) != CFDictionaryGetTypeID()) {
  		CFRelease(dict);
  		return NULL;
  	}

  	CFStringRef serversKey = CFStringCreateWithCString(kCFAllocatorDefault, "ServerAddresses", kCFStringEncodingUTF8);
  	if (!serversKey) {
  		CFRelease(dict);
  		return NULL;
  	}

  	CFArrayRef servers = (CFArrayRef)CFDictionaryGetValue((CFDictionaryRef)dict, serversKey);
  	CFRelease(serversKey);

  	if (!servers || CFGetTypeID(servers) != CFArrayGetTypeID()) {
  		CFRelease(dict);
  		return NULL;
  	}

  	CFRetain(servers);
  	CFRelease(dict);
  	return servers;
  }
  ```

- [ ] **Step 2: Update `src/dnsd/vpn_monitor.go` preamble**
  Replace the preamble to keep only header includes, standard library declarations, and extern declarations for the C functions.

  ```go
  package dnsd

  /*
  #cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation
  #include <CoreFoundation/CoreFoundation.h>
  #include <SystemConfiguration/SystemConfiguration.h>
  #include <stdlib.h>

  int start_monitoring(const char* name);
  void stop_monitoring(void);
  CFArrayRef copy_dns_servers(SCDynamicStoreRef store);
  */
  import "C"
  ```

---

### Task 2: Implement Synchronization and Double-Connection Fix in Go

**Files:**
- Modify: `src/dnsd/vpn_monitor.go`

**Interfaces:**
- Consumes: `goMonitorStarted`, `goDNSCallback`, `StartVPNMonitor`, `StopVPNMonitor`.
- Produces: Exported `goMonitorStarted` function, synchronized start/stop lifecycle with Go mutex and channel, and optimized callback.

- [ ] **Step 1: Update Go implementation in `src/dnsd/vpn_monitor.go`**
  Modify `vpn_monitor.go` to use mutex protection, synchronize via `monitorChan`, export `goMonitorStarted()`, and avoid double connections inside `goDNSCallback`.

  ```go
  var (
  	mu               sync.Mutex
  	vpnCallback      func([]string)
  	lastDNSAddresses []string
  	isMonitoring     bool
  	monitorChan      chan struct{}
  )

  //export goMonitorStarted
  func goMonitorStarted() {
  	close(monitorChan)
  }

  //export goDNSCallback
  func goDNSCallback(store C.SCDynamicStoreRef, changedKeys C.CFArrayRef, info unsafe.Pointer) {
  	// Re-use connection by calling getDNSServers directly
  	servers := getDNSServers(store)
  	if len(servers) == 0 {
  		servers = []string{"1.1.1.1"}
  	}

  	mu.Lock()
  	callback := vpnCallback
  	// Check if the servers have actually changed to avoid redundant callbacks
  	equal := len(servers) == len(lastDNSAddresses)
  	if equal {
  		for i := range servers {
  			if servers[i] != lastDNSAddresses[i] {
  				equal = false
  				break
  			}
  		}
  	}
  	if !equal {
  		lastDNSAddresses = servers
  		mu.Unlock()
  		if callback != nil {
  			callback(servers)
  		}
  	} else {
  		mu.Unlock()
  	}
  }

  // StartVPNMonitor sets up the global callback and begins monitoring the macOS SCDynamicStore
  // for network DNS changes in a background goroutine.
  func StartVPNMonitor(onUpstreamsChanged func([]string)) {
  	mu.Lock()
  	defer mu.Unlock()
  	if isMonitoring {
  		return
  	}

  	vpnCallback = onUpstreamsChanged
  	// Initialize the lastDNSAddresses with the current system DNS configuration
  	// to avoid triggering the callback immediately on startup.
  	initialServers := readDNSServers()
  	if len(initialServers) == 0 {
  		initialServers = []string{"1.1.1.1"}
  	}
  	lastDNSAddresses = initialServers

  	monitorChan = make(chan struct{})

  	go func() {
  		cName := C.CString("blackhole-dnsd")
  		defer C.free(unsafe.Pointer(cName))

  		log.Println("Monitoring SCDynamicStore for DNS shifts...")
  		C.start_monitoring(cName)
  	}()

  	// Block until goMonitorStarted is called, ensuring the run loop is fully initialized
  	<-monitorChan
  	isMonitoring = true
  }

  // StopVPNMonitor stops the background dynamic store monitoring loop.
  func StopVPNMonitor() {
  	mu.Lock()
  	defer mu.Unlock()
  	if !isMonitoring {
  		return
  	}
  	C.stop_monitoring()
  	isMonitoring = false
  }
  ```

---

### Task 3: Verify and Build Tests

**Files:**
- N/A

**Interfaces:**
- N/A

- [ ] **Step 1: Run Go test suite**
  Execute all Go tests in the package to confirm everything compiles and passes properly.
  Run: `go test -v ./src/dnsd`

---

### Task 4: Save Report and Commit

**Files:**
- Modify: `.superpowers/sdd/task-3-report.md`

- [ ] **Step 1: Append quality fixes details to the report**
  Update `/Users/thedawgctor/Desktop/dawgctor-personal-tools/blackhole/.superpowers/sdd/task-3-report.md` with:
  - Cgo preamble resolution description.
  - Run loop source leak resolution details.
  - Synchronization approach for `g_runLoop` using wait channels and `goMonitorStarted()`.
  - Double connection optimization using `getDNSServers(store)`.
  - Mutex lock protection description.
  - Test compile & run outcomes.

- [ ] **Step 2: Commit all modified files**
  Add files and commit using `git commit`.
