package client

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// ensureLoopbackAlias is a no-op on Windows. Modern Windows routes the
// full 127.0.0.0/8 loopback range without requiring explicit aliases.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

var (
	modIPHelper        = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtTCPTable = modIPHelper.NewProc("GetExtendedTcpTable")
)

// wildcardBound reports whether any process has bound 0.0.0.0:port on TCP/IPv4.
// On Windows, a wildcard socket captures connections destined for any local
// address on that port, including tela's specific loopback addresses. The
// MIB_TCPROW_OWNER_PID table stores the port in network byte order in the
// low 16 bits of a DWORD, so binary.BigEndian.Uint16 on bytes [8:10] of
// each entry gives the port in host order.
func wildcardBound(port uint16) bool {
	const tcpTableOwnerPidAll = 5

	// First call: get required buffer size.
	var size uint32
	procGetExtTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, syscall.AF_INET, tcpTableOwnerPidAll, 0)
	if size == 0 {
		return false
	}

	buf := make([]byte, size)
	ret, _, _ := procGetExtTCPTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		syscall.AF_INET,
		tcpTableOwnerPidAll,
		0,
	)
	if ret != 0 {
		return false
	}

	// MIB_TCPTABLE_OWNER_PID: DWORD dwNumEntries, then 24-byte entries.
	// Each MIB_TCPROW_OWNER_PID entry:
	//   [0..3]  dwState
	//   [4..7]  dwLocalAddr  (0 = INADDR_ANY = 0.0.0.0)
	//   [8..11] dwLocalPort  (network byte order in low 16 bits)
	//   [12..15] dwRemoteAddr
	//   [16..19] dwRemotePort
	//   [20..23] dwOwningPid
	numEntries := binary.LittleEndian.Uint32(buf[:4])
	for i := uint32(0); i < numEntries; i++ {
		base := 4 + i*24
		if base+24 > uint32(len(buf)) {
			break
		}
		localAddr := binary.LittleEndian.Uint32(buf[base+4 : base+8])
		localPort := binary.BigEndian.Uint16(buf[base+8 : base+10])
		if localAddr == 0 && localPort == port {
			return true
		}
	}
	return false
}
