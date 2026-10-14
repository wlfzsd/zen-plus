package process

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// FindByRequest returns process information for the owner of r's TCP/IPv4
// source port.
//
// Only works for local requests. Returns [ErrNotFound] if no process owns the
// port.
//
// The request context must contain [http.LocalAddrContextKey], which is set for
// requests handled by an [http.Server].
func FindByRequest(r *http.Request) (Info, error) {
	srcHost, srcPortStr, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return Info{}, fmt.Errorf("parse RemoteAddr: %v", err)
	}
	srcPort, err := strconv.ParseUint(srcPortStr, 10, 16)
	if err != nil {
		return Info{}, fmt.Errorf("parse source port: %v", err)
	}
	srcIP := net.ParseIP(srcHost)
	if srcIP == nil {
		return Info{}, fmt.Errorf("invalid source IP: %s", srcHost)
	}
	localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return Info{}, fmt.Errorf("failed to retrieve local server address from request context")
	}
	dstHost, dstPortStr, err := net.SplitHostPort(localAddr.String())
	if err != nil {
		return Info{}, fmt.Errorf("parse local address: %v", err)
	}
	dstPort, err := strconv.ParseUint(dstPortStr, 10, 16)
	if err != nil {
		return Info{}, fmt.Errorf("parse destination port: %v", err)
	}
	dstIP := net.ParseIP(dstHost)
	if dstIP == nil {
		return Info{}, fmt.Errorf("invalid destination IP: %s", dstHost)
	}

	pid, err := findPIDByIP(uint16(srcPort), uint16(dstPort), srcIP, dstIP)
	if err != nil {
		return Info{}, fmt.Errorf("find pid by IP parameters: %w", err)
	}
	info := Info{PID: pid}
	info.ExecutablePath, err = pidExecutablePath(pid)
	if err != nil {
		return info, fmt.Errorf("find executable path for pid %d: %v", pid, err)
	}

	return info, nil
}
