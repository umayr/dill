//go:build !linux

package coordination

import "net"

func CheckNamespacePeer(*net.UnixConn, NamespaceProfile) error { return ErrNamespace }
