//go:build !linux

package coordination

import "net"

func checkSharedRuntime(*net.UnixConn, NamespaceProfile, string, []RuntimeFile) error {
	return ErrNamespace
}
