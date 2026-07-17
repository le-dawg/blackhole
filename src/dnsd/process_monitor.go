package dnsd

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#import <Security/Security.h>
#import <CoreFoundation/CoreFoundation.h>
#include <sys/proc_info.h>
#include <libproc.h>
#include <stdlib.h>
#include <arpa/inet.h>
#include <sys/sysctl.h>
#include <string.h>

// Helper to extract the local port from a socket_fdinfo structure.
// Returns the local port in host byte order, or 0 if not an IP/TCP socket.
static uint16_t get_socket_local_port(struct socket_fdinfo *sockInfo) {
	if (sockInfo->psi.soi_kind == SOCKINFO_IN) {
		return ntohs(sockInfo->psi.soi_proto.pri_in.insi_lport);
	} else if (sockInfo->psi.soi_kind == SOCKINFO_TCP) {
		return ntohs(sockInfo->psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport);
	}
	return 0;
}

// Helper to check if process arguments contain "litellm" using KERN_PROCARGS2
static int check_pid_litellm(pid_t pid) {
	int mib[3];
	static int argmax = 0;
	size_t size;
	char *procargs;

	if (argmax == 0) {
		int mib_argmax[2] = {CTL_KERN, KERN_ARGMAX};
		size_t size_argmax = sizeof(argmax);
		if (sysctl(mib_argmax, 2, &argmax, &size_argmax, NULL, 0) == -1) {
			return 0;
		}
	}

	procargs = (char *)malloc(argmax);
	if (!procargs) {
		return 0;
	}

	mib[0] = CTL_KERN;
	mib[1] = KERN_PROCARGS2;
	mib[2] = pid;
	size = argmax;
	if (sysctl(mib, 3, procargs, &size, NULL, 0) == -1) {
		free(procargs);
		return 0;
	}

	if (size > 0) {
		procargs[size - 1] = '\0';
	}

	int argc;
	if (size < sizeof(argc)) {
		free(procargs);
		return 0;
	}
	memcpy(&argc, procargs, sizeof(argc));

	char *cp = procargs + sizeof(argc);
	char *end = procargs + size;

	// Skip executable path
	while (cp < end && *cp != '\0') {
		cp++;
	}
	// Skip padding nulls
	while (cp < end && *cp == '\0') {
		cp++;
	}

	int found = 0;
	for (int i = 0; i < argc; i++) {
		if (cp >= end) {
			break;
		}
		if (strstr(cp, "litellm") != NULL) {
			found = 1;
			break;
		}
		// Skip current argument
		while (cp < end && *cp != '\0') {
			cp++;
		}
		// Skip padding nulls
		while (cp < end && *cp == '\0') {
			cp++;
		}
	}

	free(procargs);
	return found;
}

// CoreFoundation/Security-based helper to retrieve the bundle ID of a process by its PID.
static char* get_bundle_id_for_pid(pid_t pid) {
	CFNumberRef pidNum = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &pid);
	if (pidNum == NULL) {
		return NULL;
	}

	const void *keys[] = { kSecGuestAttributePid };
	const void *values[] = { pidNum };
	CFDictionaryRef attributes = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(pidNum);
	if (attributes == NULL) {
		return NULL;
	}

	SecCodeRef guestRef = NULL;
	OSStatus status = SecCodeCopyGuestWithAttributes(NULL, attributes, kSecCSDefaultFlags, &guestRef);
	CFRelease(attributes);

	if (status != errSecSuccess || guestRef == NULL) {
		return NULL;
	}

	CFDictionaryRef signingInfo = NULL;
	status = SecCodeCopySigningInformation((SecStaticCodeRef)guestRef, kSecCSSigningInformation, &signingInfo);
	CFRelease(guestRef);

	if (status != errSecSuccess || signingInfo == NULL) {
		return NULL;
	}

	CFStringRef bundleIDRef = (CFStringRef)CFDictionaryGetValue(signingInfo, kSecCodeInfoIdentifier);
	if (bundleIDRef == NULL || CFGetTypeID(bundleIDRef) != CFStringGetTypeID()) {
		CFRelease(signingInfo);
		return NULL;
	}

	CFIndex length = CFStringGetLength(bundleIDRef);
	CFIndex maxSize = CFStringGetMaximumSizeForEncoding(length, kCFStringEncodingUTF8) + 1;
	char *result = (char *)malloc(maxSize);
	if (result != NULL) {
		if (!CFStringGetCString(bundleIDRef, result, maxSize, kCFStringEncodingUTF8)) {
			free(result);
			result = NULL;
		}
	}

	CFRelease(signingInfo);
	return result;
}
*/
import "C"
import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type ProcessCacheEntry struct {
	Name     string
	BundleID string
}

type ProcessMetadata struct {
	Name     string
	BundleID string
}

type portPIDEntry struct {
	pid       int
	createdAt time.Time
	err       error
}

type pidMetadataEntry struct {
	metadata  ProcessMetadata
	createdAt time.Time
	err       error
}

var (
	portToPIDCache     = make(map[uint16]portPIDEntry)
	portToPIDCacheMu   sync.RWMutex
	pidMetadataCache   = make(map[int]pidMetadataEntry)
	pidMetadataCacheMu sync.RWMutex
	metadataResolveMu  sync.Mutex

	processScanMu   sync.Mutex // For serializing system-wide scans
	bundleIDCache   = make(map[string]string)
	bundleIDCacheMu sync.RWMutex
	pidsScratch     []C.pid_t
	fdsScratch      []C.struct_proc_fdinfo
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for range ticker.C {
			now := time.Now()

			portToPIDCacheMu.Lock()
			for port, entry := range portToPIDCache {
				ttl := 5 * time.Second
				if entry.err != nil {
					ttl = 2 * time.Second
				}
				if now.Sub(entry.createdAt) >= ttl {
					delete(portToPIDCache, port)
				}
			}
			portToPIDCacheMu.Unlock()

			pidMetadataCacheMu.Lock()
			for pid, entry := range pidMetadataCache {
				if now.Sub(entry.createdAt) >= 1*time.Minute {
					delete(pidMetadataCache, pid)
				}
			}
			pidMetadataCacheMu.Unlock()
		}
	}()
}

func isLiteLLM(pid C.pid_t) bool {
	return C.check_pid_litellm(pid) != 0
}

// CheckPIDLiteLLM is a wrapper around the C helper check_pid_litellm for testing purposes.
func CheckPIDLiteLLM(pid int) bool {
	return C.check_pid_litellm(C.pid_t(pid)) != 0
}

func resolveMetadataForPID(pid int) (ProcessMetadata, error) {
	pathBuffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	ret := int(C.proc_pidpath(C.int(pid), unsafe.Pointer(&pathBuffer[0]), C.uint32_t(len(pathBuffer))))
	if ret <= 0 {
		return ProcessMetadata{}, fmt.Errorf("failed to get proc_pidpath for pid %d", pid)
	}
	procName := string(pathBuffer[:ret])

	// First query CoreFoundation/Security helper
	cBundleID := C.get_bundle_id_for_pid(C.pid_t(pid))
	var bundleID string
	if cBundleID != nil {
		bundleID = C.GoString(cBundleID)
		C.free(unsafe.Pointer(cBundleID))
	} else {
		bundleID = extractBundleID(procName)
	}

	if isLiteLLM(C.pid_t(pid)) {
		if !strings.Contains(strings.ToLower(procName), "litellm") {
			procName = procName + "-litellm"
		}
	}

	return ProcessMetadata{
		Name:     procName,
		BundleID: bundleID,
	}, nil
}

func getMetadataForPID(pid int) (string, string, error) {
	pidMetadataCacheMu.RLock()
	entry, found := pidMetadataCache[pid]
	pidMetadataCacheMu.RUnlock()

	if found && time.Since(entry.createdAt) < 1*time.Minute {
		if entry.err != nil {
			return "", "", entry.err
		}
		return entry.metadata.Name, entry.metadata.BundleID, nil
	}

	metadataResolveMu.Lock()
	defer metadataResolveMu.Unlock()

	// Double check
	pidMetadataCacheMu.RLock()
	entry, found = pidMetadataCache[pid]
	pidMetadataCacheMu.RUnlock()

	if found && time.Since(entry.createdAt) < 1*time.Minute {
		if entry.err != nil {
			return "", "", entry.err
		}
		return entry.metadata.Name, entry.metadata.BundleID, nil
	}

	meta, err := resolveMetadataForPID(pid)

	pidMetadataCacheMu.Lock()
	pidMetadataCache[pid] = pidMetadataEntry{
		metadata:  meta,
		createdAt: time.Now(),
		err:       err,
	}
	pidMetadataCacheMu.Unlock()

	if err != nil {
		return "", "", err
	}
	return meta.Name, meta.BundleID, nil
}

// GetProcessInfoForPort queries the system APIs to map an active TCP/UDP local port
// to its originating Process Name, Bundle ID (if applicable), and PID.
func GetProcessInfoForPort(port uint16) (string, string, error) {
	portToPIDCacheMu.RLock()
	entry, found := portToPIDCache[port]
	portToPIDCacheMu.RUnlock()

	if found {
		ttl := 5 * time.Second
		if entry.err != nil {
			ttl = 2 * time.Second
		}
		if time.Since(entry.createdAt) < ttl {
			if entry.err != nil {
				return "", "", entry.err
			}
			return getMetadataForPID(entry.pid)
		}
	}

	processScanMu.Lock()
	defer processScanMu.Unlock()

	// Double-check under scan lock
	portToPIDCacheMu.RLock()
	entry, found = portToPIDCache[port]
	portToPIDCacheMu.RUnlock()

	if found {
		ttl := 5 * time.Second
		if entry.err != nil {
			ttl = 2 * time.Second
		}
		if time.Since(entry.createdAt) < ttl {
			if entry.err != nil {
				return "", "", entry.err
			}
			return getMetadataForPID(entry.pid)
		}
	}

	pid, err := getProcessInfoForPortNoCache(port)
	if err != nil {
		portToPIDCacheMu.Lock()
		portToPIDCache[port] = portPIDEntry{
			createdAt: time.Now(),
			err:       err,
		}
		portToPIDCacheMu.Unlock()
		return "", "", err
	}

	return getMetadataForPID(pid)
}

func getProcessInfoForPortNoCache(port uint16) (int, error) {
	// Retrieve list of pids running on system (returns size in bytes)
	bytesCount := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
	if bytesCount <= 0 {
		return 0, errors.New("failed to list pids")
	}

	// Calculate number of PIDs from returned byte size
	pidSize := C.int(unsafe.Sizeof(C.pid_t(0)))
	pidsCount := bytesCount / pidSize
	if pidsCount <= 0 {
		return 0, errors.New("failed to list pids: no pids allocated")
	}

	if int(pidsCount) > cap(pidsScratch) {
		pidsScratch = make([]C.pid_t, pidsCount)
	} else {
		pidsScratch = pidsScratch[:pidsCount]
	}

	resPids := C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pidsScratch[0]), bytesCount)
	if resPids <= 0 {
		return 0, errors.New("failed to list pids")
	}
	actualPidsCount := int(resPids) / int(pidSize)
	pids := pidsScratch
	if actualPidsCount < len(pids) {
		pids = pids[:actualPidsCount]
	}

	var targetPID int
	var targetFound bool

	foundMappings := make(map[uint16]int)

	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		// Query file descriptor info for sockets
		fdBufferSize := C.proc_pidinfo(C.int(pid), C.PROC_PIDLISTFDS, 0, nil, 0)
		if fdBufferSize <= 0 {
			continue
		}

		fdCount := int(fdBufferSize) / int(unsafe.Sizeof(C.struct_proc_fdinfo{}))
		if fdCount <= 0 {
			continue
		}

		if fdCount > cap(fdsScratch) {
			fdsScratch = make([]C.struct_proc_fdinfo, fdCount)
		} else {
			fdsScratch = fdsScratch[:fdCount]
		}

		resFd := C.proc_pidinfo(C.int(pid), C.PROC_PIDLISTFDS, 0, unsafe.Pointer(&fdsScratch[0]), C.int(fdBufferSize))
		if resFd <= 0 {
			continue
		}
		actualFdCount := int(resFd) / int(unsafe.Sizeof(C.struct_proc_fdinfo{}))
		fds := fdsScratch
		if actualFdCount < len(fds) {
			fds = fds[:actualFdCount]
		}

		for _, fd := range fds {
			if fd.proc_fdtype == C.PROX_FDTYPE_SOCKET {
				var sockInfo C.struct_socket_fdinfo
				sockSize := C.proc_pidfdinfo(C.int(pid), fd.proc_fd, C.PROC_PIDFDSOCKETINFO, unsafe.Pointer(&sockInfo), C.int(unsafe.Sizeof(sockInfo)))
				if sockSize > 0 {
					// Use C helper to extract local port from the union in a compilation-safe way
					localPort := uint16(C.get_socket_local_port(&sockInfo))
					if localPort > 0 {
						foundMappings[localPort] = int(pid)
						if localPort == port {
							targetPID = int(pid)
							targetFound = true
						}
					}
				}
			}
		}
	}

	// Update the portToPIDCache with all found mappings
	portToPIDCacheMu.Lock()
	for p, pidVal := range foundMappings {
		portToPIDCache[p] = portPIDEntry{
			pid:       pidVal,
			createdAt: time.Now(),
		}
	}
	portToPIDCacheMu.Unlock()

	if targetFound {
		return targetPID, nil
	}
	return 0, fmt.Errorf("port %d not found in active sockets", port)
}

func extractBundleID(execPath string) string {
	bundleIDCacheMu.RLock()
	cached, found := bundleIDCache[execPath]
	bundleIDCacheMu.RUnlock()
	if found {
		return cached
	}

	idx := strings.LastIndex(strings.ToLower(execPath), ".app/")
	if idx == -1 {
		return ""
	}
	appPath := execPath[:idx+4] // e.g. /Applications/Example.app

	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(plistPath); err != nil {
		plistPath = filepath.Join(appPath, "Info.plist")
		if _, err := os.Stat(plistPath); err != nil {
			return ""
		}
	}

	data, err := os.ReadFile(plistPath)
	if err != nil {
		return ""
	}

	if bytes.HasPrefix(data, []byte("bplist")) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", plistPath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			data = out.Bytes()
		}
	}

	resolved := parsePlistXML(data)

	bundleIDCacheMu.Lock()
	bundleIDCache[execPath] = resolved
	bundleIDCacheMu.Unlock()

	return resolved
}

func parsePlistXML(data []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var lastKey string
	for {
		t, err := decoder.Token()
		if err != nil {
			break
		}
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "key" {
				var key string
				if err := decoder.DecodeElement(&key, &se); err == nil {
					lastKey = key
				}
			} else if se.Name.Local == "string" {
				var val string
				if err := decoder.DecodeElement(&val, &se); err == nil {
					if lastKey == "CFBundleIdentifier" {
						return val
					}
				}
			}
		}
	}
	return ""
}
