package dnsd

/*
#cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>

// Forward declaration of the exported Go function
void goDNSCallback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info);

static void my_callback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info) {
	goDNSCallback(store, changedKeys, info);
}

static CFRunLoopRef g_runLoop = NULL;

static int start_monitoring(const char* name) {
	CFStringRef nameStr = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingUTF8);
	if (!nameStr) return -1;

	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
	SCDynamicStoreRef store = SCDynamicStoreCreate(kCFAllocatorDefault, nameStr, my_callback, &context);
	CFRelease(nameStr);
	if (!store) return -1;

	CFStringRef pattern = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
	if (!pattern) {
		CFRelease(store);
		return -1;
	}

	CFArrayRef keys = CFArrayCreate(kCFAllocatorDefault, (const void **)&pattern, 1, &kCFTypeArrayCallBacks);
	CFRelease(pattern);
	if (!keys) {
		CFRelease(store);
		return -1;
	}

	SCDynamicStoreSetNotificationKeys(store, keys, NULL);
	CFRelease(keys);

	CFRunLoopSourceRef rls = SCDynamicStoreCreateRunLoopSource(kCFAllocatorDefault, store, 0);
	CFRelease(store);
	if (!rls) return -1;

	g_runLoop = CFRunLoopGetCurrent();
	CFRunLoopAddSource(g_runLoop, rls, kCFRunLoopCommonModes);
	CFRelease(rls);

	CFRunLoopRun();
	return 0;
}

static void stop_monitoring() {
	if (g_runLoop) {
		CFRunLoopStop(g_runLoop);
		g_runLoop = NULL;
	}
}

static CFArrayRef copy_dns_servers(SCDynamicStoreRef store) {
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
*/
import "C"
import (
	"log"
	"sync"
	"unsafe"
)

var (
	mu               sync.Mutex
	vpnCallback      func([]string)
	lastDNSAddresses []string
)

//export goDNSCallback
func goDNSCallback(store C.SCDynamicStoreRef, changedKeys C.CFArrayRef, info unsafe.Pointer) {
	servers := readDNSServers()
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

// readDNSServers reads the current system DNS server configuration from SCDynamicStore
func readDNSServers() []string {
	cName := C.CString("blackhole-dns-reader")
	defer C.free(unsafe.Pointer(cName))

	nameStr := C.CFStringCreateWithCString(C.kCFAllocatorDefault, cName, C.kCFStringEncodingUTF8)
	if nameStr == nil {
		return nil
	}
	defer C.CFRelease(C.CFTypeRef(nameStr))

	store := C.SCDynamicStoreCreate(C.kCFAllocatorDefault, nameStr, nil, nil)
	if store == nil {
		return nil
	}
	defer C.CFRelease(C.CFTypeRef(store))

	return getDNSServers(store)
}

// getDNSServers extracts DNS server IP addresses from the given SCDynamicStoreRef
func getDNSServers(store C.SCDynamicStoreRef) []string {
	serversArray := C.copy_dns_servers(store)
	if serversArray == nil {
		return nil
	}
	defer C.CFRelease(C.CFTypeRef(serversArray))

	count := C.CFArrayGetCount(serversArray)
	if count <= 0 {
		return nil
	}

	var servers []string
	for i := C.CFIndex(0); i < count; i++ {
		val := C.CFArrayGetValueAtIndex(serversArray, i)
		if val == nil {
			continue
		}
		if C.CFGetTypeID(val) == C.CFStringGetTypeID() {
			cfStr := C.CFStringRef(val)
			cStr := C.CFStringGetCStringPtr(cfStr, C.kCFStringEncodingUTF8)
			if cStr != nil {
				servers = append(servers, C.GoString(cStr))
			} else {
				length := C.CFStringGetLength(cfStr)
				maxSize := C.CFStringGetMaximumSizeForEncoding(length, C.kCFStringEncodingUTF8) + 1
				buffer := make([]C.char, maxSize)
				if C.CFStringGetCString(cfStr, &buffer[0], maxSize, C.kCFStringEncodingUTF8) != 0 {
					servers = append(servers, C.GoString(&buffer[0]))
				}
			}
		}
	}
	return servers
}

// StartVPNMonitor sets up the global callback and begins monitoring the macOS SCDynamicStore
// for network DNS changes in a background goroutine.
func StartVPNMonitor(onUpstreamsChanged func([]string)) {
	mu.Lock()
	vpnCallback = onUpstreamsChanged
	// Initialize the lastDNSAddresses with the current system DNS configuration
	// to avoid triggering the callback immediately on startup.
	initialServers := readDNSServers()
	if len(initialServers) == 0 {
		initialServers = []string{"1.1.1.1"}
	}
	lastDNSAddresses = initialServers
	mu.Unlock()

	go func() {
		cName := C.CString("blackhole-dnsd")
		defer C.free(unsafe.Pointer(cName))

		log.Println("Monitoring SCDynamicStore for DNS shifts...")
		C.start_monitoring(cName)
	}()
}

// StopVPNMonitor stops the background dynamic store monitoring loop.
func StopVPNMonitor() {
	C.stop_monitoring()
}
