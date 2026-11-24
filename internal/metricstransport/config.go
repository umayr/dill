package metricstransport

import (
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func validateConfig(cfg Config) (string, error) {
	bind, err := netip.ParseAddrPort(cfg.BindAddress)
	if err != nil || bind.Port() == 0 || bind.Addr().Zone() != "" {
		return "", errors.New("metrics requires an explicit private or loopback IP and nonzero port")
	}
	ip := bind.Addr().Unmap()
	if !ip.IsPrivate() && !ip.IsLoopback() {
		return "", errors.New("metrics requires an explicit private or loopback IP and nonzero port")
	}
	for _, reserved := range cfg.ReservedAddresses {
		otherIP, otherPort, ok := reservedAddress(reserved)
		if !ok {
			return "", errors.New("cannot validate a reserved metrics bind address")
		}
		if otherPort == bind.Port() && (otherIP.IsUnspecified() || otherIP == ip) {
			return "", errors.New("metrics bind overlaps a reserved endpoint")
		}
	}
	if !validServerName(cfg.ServerName) {
		return "", errors.New("metrics requires an explicit TLS DNS name or IP")
	}
	if cfg.ExpectedDaemonID == "" || len(cfg.ExpectedDaemonID) > 256 ||
		strings.ContainsFunc(cfg.ExpectedDaemonID, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", errors.New("metrics requires an enrolled daemon ID")
	}
	paths := make(map[string]bool, 4)
	for _, path := range []string{cfg.CAFile, cfg.CertFile, cfg.KeyFile, cfg.TokenFile} {
		if !validCredentialPath(path) || paths[path] {
			return "", errors.New("metrics credentials require distinct absolute file paths")
		}
		paths[path] = true
	}
	for _, path := range cfg.ReservedCredentialFiles {
		if !validCredentialPath(path) || paths[path] {
			return "", errors.New("metrics credentials overlap reserved files or reserved paths are invalid")
		}
	}
	return netip.AddrPortFrom(ip, bind.Port()).String(), nil
}

func reservedAddress(s string) (netip.Addr, uint16, bool) {
	host, portText, err := net.SplitHostPort(s)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return netip.Addr{}, 0, false
	}
	if host == "" {
		return netip.IPv6Unspecified(), uint16(port), true
	}
	ip, err := netip.ParseAddr(host)
	return ip.Unmap(), uint16(port), err == nil && ip.Zone() == ""
}

func validServerName(name string) bool {
	if ip, err := netip.ParseAddr(name); err == nil {
		return ip.Zone() == "" && !ip.IsUnspecified()
	}
	if name == "" || len(name) > 253 {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validCredentialPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && len(path) <= 4096 && !strings.ContainsRune(path, 0)
}
