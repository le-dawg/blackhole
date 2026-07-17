package dnsd

/*
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
static int check_pid_litellm(int pid) {
	int mib[3];
	int argmax;
	size_t size;
	char *procargs;

	mib[0] = CTL_KERN;
	mib[1] = KERN_ARGMAX;
	size = sizeof(argmax);
	if (sysctl(mib, 2, &argmax, &size, NULL, 0) == -1) {
		return 0;
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
*/
import "C"
import (
	"bytes"
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

type cacheEntry struct {
	name      string
	bundleID  string
	createdAt time.Time
	err       error
}

var (
	processCache    = make(map[uint16]cacheEntry)
	processCacheMu  sync.RWMutex
	processScanMu   sync.Mutex // For serializing system-wide scans
	bundleIDCache   = make(map[string]string)
	bundleIDCacheMu sync.RWMutex
)

// CheckPIDLiteLLM is a wrapper around the C helper check_pid_litellm for testing purposes.
func CheckPIDLiteLLM(pid int) bool {
	return C.check_pid_litellm(C.int(pid)) != 0
}

// GetProcessInfoForPort queries the system APIs to map an active TCP/UDP local port
// to its originating Process Name, Bundle ID (if applicable), and PID.
func GetProcessInfoForPort(port uint16) (string, string, error) {
	processCacheMu.RLock()
	entry, found := processCache[port]
	processCacheMu.RUnlock()

	if found {
		ttl := 5 * time.Second
		if entry.err != nil {
			ttl = 2 * time.Second
		}
		if time.Since(entry.createdAt) < ttl {
			if entry.err != nil {
				return "", "", entry.err
			}
			return entry.name, entry.bundleID, nil
		}
	}

	processScanMu.Lock()
	defer processScanMu.Unlock()

	// Double-check under scan lock
	processCacheMu.RLock()
	entry, found = processCache[port]
	processCacheMu.RUnlock()

	if found {
		ttl := 5 * time.Second
		if entry.err != nil {
			ttl = 2 * time.Second
		}
		if time.Since(entry.createdAt) < ttl {
			if entry.err != nil {
				return "", "", entry.err
			}
			return entry.name, entry.bundleID, nil
		}
	}

	name, bundleID, err := getProcessInfoForPortNoCache(port)

	processCacheMu.Lock()
	if err != nil {
		processCache[port] = cacheEntry{
			createdAt: time.Now(),
			err:       err,
		}
		processCacheMu.Unlock()
		return "", "", err
	}

	processCache[port] = cacheEntry{
		name:      name,
		bundleID:  bundleID,
		createdAt: time.Now(),
	}
	processCacheMu.Unlock()

	return name, bundleID, nil
}

func getProcessInfoForPortNoCache(port uint16) (string, string, error) {
	// Retrieve list of pids running on system (returns size in bytes)
	bytesCount := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
	if bytesCount <= 0 {
		return "", "", errors.New("failed to list pids")
	}

	// Calculate number of PIDs from returned byte size
	pidSize := C.int(unsafe.Sizeof(C.int(0)))
	pidsCount := bytesCount / pidSize
	if pidsCount <= 0 {
		return "", "", errors.New("failed to list pids: no pids allocated")
	}

	pids := make([]C.int, pidsCount)
	if len(pids) > 0 {
		resPids := C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), bytesCount)
		if resPids <= 0 {
			return "", "", errors.New("failed to list pids")
		}
		actualPidsCount := int(resPids) / int(pidSize)
		if actualPidsCount < len(pids) {
			pids = pids[:actualPidsCount]
		}
	} else {
		return "", "", errors.New("failed to list pids: no pids allocated")
	}

	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		// Query file descriptor info for sockets
		fdBufferSize := C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, nil, 0)
		if fdBufferSize <= 0 {
			continue
		}

		fdCount := int(fdBufferSize) / int(unsafe.Sizeof(C.struct_proc_fdinfo{}))
		if fdCount <= 0 {
			continue
		}
		fds := make([]C.struct_proc_fdinfo, fdCount)
		if len(fds) > 0 {
			resFd := C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, unsafe.Pointer(&fds[0]), C.int(fdBufferSize))
			if resFd <= 0 {
				continue
			}
			actualFdCount := int(resFd) / int(unsafe.Sizeof(C.struct_proc_fdinfo{}))
			if actualFdCount < len(fds) {
				fds = fds[:actualFdCount]
			}
		} else {
			continue
		}

		for _, fd := range fds {
			if fd.proc_fdtype == C.PROX_FDTYPE_SOCKET {
				var sockInfo C.struct_socket_fdinfo
				sockSize := C.proc_pidfdinfo(pid, fd.proc_fd, C.PROC_PIDFDSOCKETINFO, unsafe.Pointer(&sockInfo), C.int(unsafe.Sizeof(sockInfo)))
				if sockSize > 0 {
					// Use C helper to extract local port from the union in a compilation-safe way
					localPort := C.get_socket_local_port(&sockInfo)
					if uint16(localPort) == port {
						// Match! Find process executable path
						pathBuffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
						ret := C.proc_pidpath(pid, unsafe.Pointer(&pathBuffer[0]), C.uint32_t(len(pathBuffer)))
						if ret > 0 {
							procName := C.GoString((*C.char)(unsafe.Pointer(&pathBuffer[0])))
							bundleID := extractBundleID(procName)

							if C.check_pid_litellm(pid) != 0 {
								if !strings.Contains(strings.ToLower(procName), "litellm") {
									procName = procName + "-litellm"
								}
							}
							return procName, bundleID, nil
						}
					}
				}
			}
		}
	}
	return "", "", fmt.Errorf("port %d not found in active sockets", port)
}

func extractBundleID(execPath string) string {
	bundleIDCacheMu.RLock()
	cached, found := bundleIDCache[execPath]
	bundleIDCacheMu.RUnlock()
	if found {
		return cached
	}

	idx := strings.Index(execPath, ".app/")
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
		cmd := exec.Command("plutil", "-convert", "xml1", "-o", "-", plistPath)
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
