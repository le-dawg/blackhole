package dnsd

/*
#include <sys/proc_info.h>
#include <libproc.h>
#include <stdlib.h>
#include <arpa/inet.h>

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
*/
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

type ProcessCacheEntry struct {
	Name     string
	BundleID string
}

// GetProcessInfoForPort queries the system APIs to map an active TCP/UDP local port
// to its originating Process Name, Bundle ID (if applicable), and PID.
func GetProcessInfoForPort(port uint16) (string, string, error) {
	// Retrieve list of pids running on system (returns size in bytes)
	bytesCount := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
	if bytesCount <= 0 {
		return "", "", errors.New("failed to list pids")
	}

	// Calculate number of PIDs from returned byte size
	pidSize := C.int(unsafe.Sizeof(C.int(0)))
	pidsCount := bytesCount / pidSize

	pids := make([]C.int, pidsCount)
	C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), bytesCount)

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
		fds := make([]C.struct_proc_fdinfo, fdCount)
		C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, unsafe.Pointer(&fds[0]), C.int(fdBufferSize))

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
							return procName, "", nil
						}
					}
				}
			}
		}
	}
	return "", "", fmt.Errorf("port %d not found in active sockets", port)
}
