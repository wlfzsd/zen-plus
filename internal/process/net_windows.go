package process

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func findPIDByIP(srcPort, dstPort uint16, srcIP, dstIP net.IP) (PID, error) {
	if srcPort == 0 {
		return 0, ErrNotFound
	}

	// 2026-10-07 效率审计：四元组缓存命中即免全表枚举（TIME_WAIT 保证键的
	// 生命周期内 PID 不变）。
	pid, err := cachedPIDByIP(srcPort, dstPort, srcIP, dstIP)
	if err != nil {
		return 0, err
	}

	return PID(pid), nil
}

func findPidByPort(port uint16) (uint32, error) {
	tcpTable, err := getTCPTable()
	if err != nil {
		return 0, fmt.Errorf("get tcp table: %v", err)
	}

	// Pre-convert to network byte order.
	netPort := port<<8 | port>>8

	for _, r := range tcpTable {
		if uint16(r.dwLocalPort) == netPort { // #nosec G115 -- port numbers always fit in uint16
			return r.dwOwningPid, nil
		}
	}
	return 0, ErrNotFound
}

func getTCPTable() ([]mibTcpRowOwnerPid, error) {
	var bufSize uint32
	ret := getExtendedTcpTable(nil, &bufSize, false, windows.AF_INET, tcpTableOwnerPidAll, 0)
	if ret != uint32(windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, fmt.Errorf("GetExtendedTcpTable size query: %w", syscall.Errno(ret))
	}

	for {
		table := make([]byte, bufSize)
		ret = getExtendedTcpTable(&table[0], &bufSize, false, windows.AF_INET, tcpTableOwnerPidAll, 0)
		switch ret {
		case 0:
			dwNumEntries := int(*(*uint32)(unsafe.Pointer(&table[0])))
			return unsafe.Slice((*mibTcpRowOwnerPid)(unsafe.Pointer(&table[mibTcpTableOwnerPidTableOffset])), dwNumEntries), nil
		case uint32(windows.ERROR_INSUFFICIENT_BUFFER):
			continue
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", syscall.Errno(ret))
		}
	}
}
