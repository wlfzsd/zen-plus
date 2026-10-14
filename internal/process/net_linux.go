//go:build linux

package process

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"syscall"

	"golang.org/x/sys/unix"
)

// Offsets/sizes from struct inet_diag_msg in linux/inet_diag.h:
// https://github.com/torvalds/linux/blob/248951ddc14de84de3910f9b13f51491a8cd91df/include/uapi/linux/inet_diag.h#L117
const (
	// Layout of nlmsghdr + inet_diag_req_v2 for AF_INET/TCP requests.
	inetDiagReqMsgSize = 72

	nlMsgLenOffset   = 0
	nlMsgTypeOffset  = 4
	nlMsgFlagsOffset = 6
	nlMsgSeqOffset   = 8

	inetDiagReqFamilyOffset   = 16
	inetDiagReqProtocolOffset = 17
	inetDiagReqStatesOffset   = 20
	inetDiagReqSrcPortOffset  = 24
	inetDiagReqDstPortOffset  = 26
	inetDiagReqSrcAddrOffset  = 28
	inetDiagReqDstAddrOffset  = 44
	inetDiagReqCookie0Offset  = 64
	inetDiagReqCookie1Offset  = 68

	inetDiagMsgInodeOffset = 68
	inetDiagMsgInodeSize   = 4
	inetDiagMsgMinSize     = inetDiagMsgInodeOffset + inetDiagMsgInodeSize
)

// findPIDByIP finds the PID of the process that owns the TCP connection specified by the source and destination IP addresses and ports.
func findPIDByIP(srcPort, dstPort uint16, srcIP, dstIP net.IP) (PID, error) {
	inode, err := findInode(srcPort, dstPort, srcIP, dstIP)
	if err != nil {
		return 0, fmt.Errorf("find inode by netlink: %w", err)
	}

	pid, err := findPID(inode)
	if err != nil {
		return 0, fmt.Errorf("find pid: %w", err)
	}

	return pid, nil
}

// findInode finds the inode number of the socket associated with the given source and destination IP addresses and ports using netlink.
func findInode(srcPort, dstPort uint16, srcIP, dstIP net.IP) (uint64, error) {

	// Create a netlink socket to communicate with the kernel
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return 0, fmt.Errorf("create netlink socket: %v", err)
	}
	defer unix.Close(fd)

	ip4Src := srcIP.To4()
	ip4Dst := dstIP.To4()
	if ip4Src == nil || ip4Dst == nil {
		return 0, fmt.Errorf("only IPv4 addresses are supported")
	}

	// Build nlmsghdr + inet_diag_req_v2 in a fixed-size stack buffer
	var msg [inetDiagReqMsgSize]byte
	binary.NativeEndian.PutUint32(msg[nlMsgLenOffset:nlMsgLenOffset+4], inetDiagReqMsgSize)
	binary.NativeEndian.PutUint16(msg[nlMsgTypeOffset:nlMsgTypeOffset+2], unix.SOCK_DIAG_BY_FAMILY)
	binary.NativeEndian.PutUint16(msg[nlMsgFlagsOffset:nlMsgFlagsOffset+2], unix.NLM_F_REQUEST)
	binary.NativeEndian.PutUint32(msg[nlMsgSeqOffset:nlMsgSeqOffset+4], 1)

	msg[inetDiagReqFamilyOffset] = unix.AF_INET
	msg[inetDiagReqProtocolOffset] = unix.IPPROTO_TCP
	binary.NativeEndian.PutUint32(msg[inetDiagReqStatesOffset:inetDiagReqStatesOffset+4], 0xffffffff)
	binary.BigEndian.PutUint16(msg[inetDiagReqSrcPortOffset:inetDiagReqSrcPortOffset+2], srcPort)
	binary.BigEndian.PutUint16(msg[inetDiagReqDstPortOffset:inetDiagReqDstPortOffset+2], dstPort)
	copy(msg[inetDiagReqSrcAddrOffset:inetDiagReqSrcAddrOffset+4], ip4Src)
	copy(msg[inetDiagReqDstAddrOffset:inetDiagReqDstAddrOffset+4], ip4Dst)
	binary.NativeEndian.PutUint32(msg[inetDiagReqCookie0Offset:inetDiagReqCookie0Offset+4], 0xffffffff)
	binary.NativeEndian.PutUint32(msg[inetDiagReqCookie1Offset:inetDiagReqCookie1Offset+4], 0xffffffff)

	// Send the netlink request to the kernel
	sa := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Sendto(fd, msg[:], 0, sa); err != nil {
		return 0, fmt.Errorf("send netlink request: %v", err)
	}

	// Receive the response from the kernel
	resBuf := make([]byte, 16384)
	n, from, err := unix.Recvfrom(fd, resBuf, 0)

	if err != nil {
		return 0, fmt.Errorf("receive netlink response: %v", err)
	}

	// Validate that the response is from the kernel (pid 0)
	nlFrom, ok := from.(*unix.SockaddrNetlink)
	if !ok {
		return 0, fmt.Errorf("unexpected socket address type: %T", from)
	}
	if nlFrom.Pid != 0 {
		return 0, fmt.Errorf("unexpected netlink response from non-kernel source (pid: %d)", nlFrom.Pid)
	}

	// Parse the netlink messages from the response buffer
	messages, err := syscall.ParseNetlinkMessage(resBuf[:n])
	if err != nil {
		return 0, fmt.Errorf("parse netlink message: %v", err)
	}

	// Iterate through the netlink messages to find the inode of the socket
	for _, msg := range messages {
		if msg.Header.Type == unix.NLMSG_DONE {
			break
		}

		if msg.Header.Type == unix.NLMSG_ERROR {
			if len(msg.Data) < 4 {
				return 0, fmt.Errorf("netlink error: missing data")
			}

			errno := int32(binary.NativeEndian.Uint32(msg.Data[:4])) // #nosec G115 -- nlmsgerr.error is a signed int32 reinterpreted from raw bytes
			if errno == 0 {
				continue
			}

			if errno > 0 {
				return 0, fmt.Errorf("unexpected positive netlink errno: %d", errno)
			}

			kernelErr := unix.Errno(-errno)
			if kernelErr == unix.ENOENT {
				return 0, ErrNotFound
			}
			return 0, fmt.Errorf("netlink kernel error: %w", kernelErr)

		}

		// Check if the message is of type SOCK_DIAG_BY_FAMILY and extract the inode from the message data.
		if msg.Header.Type == unix.SOCK_DIAG_BY_FAMILY {
			if len(msg.Data) < inetDiagMsgMinSize {
				continue
			}

			inode := binary.NativeEndian.Uint32(msg.Data[inetDiagMsgInodeOffset : inetDiagMsgInodeOffset+inetDiagMsgInodeSize])
			if inode != 0 {
				return uint64(inode), nil
			}
		}
	}

	return 0, ErrNotFound
}

// findPID finds the PID of the process that owns the socket with the given inode by scanning the /proc filesystem.
func findPID(inode uint64) (PID, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}

	target := fmt.Sprintf("socket:[%d]", inode)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil {
			continue // Not a PID directory.
		}

		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // Permission denied or process gone.
		}

		for _, fd := range fds {
			if fd.Type() != fs.ModeSymlink {
				continue
			}

			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == target {
				return PID(pid), nil
			}
		}
	}
	return 0, ErrNotFound
}
