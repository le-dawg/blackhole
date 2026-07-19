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
import (
	"fmt"
	"log"
	"sync"
	"unsafe"
)

var (
	mu               sync.Mutex
	vpnCallback      func([]string)
	lastDNSAddresses []string
	isMonitoring     bool
	monitorChan      chan int
)

//export goMonitorStarted
func goMonitorStarted(status C.int) {
	select {
	case monitorChan <- int(status):
	default:
	}
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

// readDNSServers reads the current system DNS server configuration from SCDynamicStore
func readDNSServers() []string {
	cName := C.CString("blackhole-dns-reader")
	defer C.free(unsafe.Pointer(cName))

	nameStr := C.CFStringCreateWithCString(C.kCFAllocatorDefault, cName, C.kCFStringEncodingUTF8)
	if unsafe.Pointer(nameStr) == nil {
		return nil
	}
	defer C.CFRelease(C.CFTypeRef(nameStr))

	store := C.SCDynamicStoreCreate(C.kCFAllocatorDefault, nameStr, nil, nil)
	if unsafe.Pointer(store) == nil {
		return nil
	}
	defer C.CFRelease(C.CFTypeRef(store))

	return getDNSServers(store)
}

// getDNSServers extracts DNS server IP addresses from the given SCDynamicStoreRef
func getDNSServers(store C.SCDynamicStoreRef) []string {
	serversArray := C.copy_dns_servers(store)
	if unsafe.Pointer(serversArray) == nil {
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
		if C.CFGetTypeID(C.CFTypeRef(val)) == C.CFStringGetTypeID() {
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
func StartVPNMonitor(onUpstreamsChanged func([]string)) error {
	mu.Lock()
	defer mu.Unlock()
	if isMonitoring {
		return nil
	}

	vpnCallback = onUpstreamsChanged
	// Initialize the lastDNSAddresses with the current system DNS configuration
	// to avoid triggering the callback immediately on startup.
	initialServers := readDNSServers()
	if len(initialServers) == 0 {
		initialServers = []string{"1.1.1.1"}
	}
	lastDNSAddresses = initialServers

	monitorChan = make(chan int, 1)

	go func() {
		cName := C.CString("blackhole-dnsd")
		defer C.free(unsafe.Pointer(cName))

		log.Println("Monitoring SCDynamicStore for DNS shifts...")
		C.start_monitoring(cName)
	}()

	// Block until goMonitorStarted is called, ensuring the run loop is fully initialized
	status := <-monitorChan
	if status != 0 {
		vpnCallback = nil
		return fmt.Errorf("failed to start SCDynamicStore monitor: C status %d", status)
	}
	isMonitoring = true
	return nil
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
	vpnCallback = nil
}
