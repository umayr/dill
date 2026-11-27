//go:build !linux

package coordination

import "net"

func checkPodmanPeer(*net.UnixConn, PodmanQualification) error { return ErrNamespace }
