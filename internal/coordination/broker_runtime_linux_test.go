//go:build linux

package coordination

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestBrokerRuntimeNamespaceEvidence(t *testing.T) {
	path := os.Getenv("BROKER_TEST_PROFILE")
	if path == "" {
		t.Skip("explicit operator runtime profile required")
	}
	p, err := ReadBrokerProfile(path)
	if err != nil || p.Podman == nil {
		t.Fatal("profile", err)
	}
	q := *p.Podman
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: q.Socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw, _ := conn.SyscallConn()
	var cred *unix.Ucred
	var ce error
	raw.Control(func(fd uintptr) { cred, ce = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if ce != nil || cred == nil {
		t.Fatal("peer credentials", ce)
	}
	self, se := processNamespace(os.Getpid(), q.Namespace.Rootless)
	peer, pe := processNamespace(int(cred.Pid), q.Namespace.Rootless)
	t.Logf("executor uid=%d gid=%d peer pid/uid/gid=%d/%d/%d self=%+v err=%v peer=%+v err=%v expected=%+v", os.Geteuid(), os.Getegid(), cred.Pid, cred.Uid, cred.Gid, self, se, peer, pe, q.Namespace)
	if err = checkPodmanPeer(conn, q); err != nil {
		t.Fatal("kernel peer check", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	observed, e := newRetainedTarget(ctx, q.Socket)
	if e != nil {
		t.Fatal("retained target", e)
	}
	defer observed.Close()
	client := &http.Client{Transport: &podmanTransport{inner: observed.transport}}
	res, e := client.Get("http://d/v5.0.0/libpod/info")
	if e != nil {
		t.Fatal("retained HTTP info", e)
	}
	body, e := io.ReadAll(res.Body)
	res.Body.Close()
	var metadata struct {
		Host *struct {
			Hostname, OS string
			Security     *struct{ Rootless *bool }
		}
		Store   *struct{ GraphRoot string }
		Version struct{ APIVersion string }
	}
	decodeErr := json.Unmarshal(body, &metadata)
	t.Logf("info status=%d length=%d read=%v decode=%v metadata=%+v", res.StatusCode, len(body), e, decodeErr, metadata)
	if metadata.Host != nil {
		t.Logf("host=%+v", *metadata.Host)
	}
	if metadata.Store != nil {
		t.Logf("store=%+v", *metadata.Store)
	}
	target, err := QualifyPodman(ctx, q)
	if err != nil {
		t.Fatal("live info qualification", err)
	}
	target.Close()
}
