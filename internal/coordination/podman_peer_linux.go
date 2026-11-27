//go:build linux

package coordination

import (
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"strings"
)

func checkPodmanPeer(conn *net.UnixConn, q PodmanQualification) error {
	if CheckNamespacePeer(conn, q.Namespace) != nil {
		return ErrNamespace
	}
	f, err := os.OpenFile("/etc/machine-id", os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrNamespace
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 34))
	if err != nil || len(data) > 33 || strings.TrimSuffix(string(data), "\n") != q.MachineID {
		return ErrNamespace
	}
	return nil
}
