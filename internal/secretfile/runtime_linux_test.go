//go:build linux

package secretfile

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func TestMappingFromDockerInfo(t *testing.T) {
	maps := Mapping{UID: []IDRange{{0, 0, 4294967295}}, GID: []IDRange{{0, 0, 4294967295}}}
	valid := `{"OSType":"linux","OperatingSystem":"Ubuntu 24.04","ServerVersion":"28.0.0","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`
	if _, err := mappingFromInfo("docker", "/run/docker.sock", []byte(valid), unix.Ucred{}, 1000, maps); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"linux"`, `"windows"`, 1), strings.Replace(valid, `Ubuntu 24.04`, `Docker Desktop`, 1), strings.Replace(valid, `28.0.0`, ``, 1),
		strings.Replace(valid, `"name=cgroupns"`, `"name=rootless"`, 1), strings.Replace(valid, `"name=cgroupns"`, `"name=userns"`, 1),
		`{"OSType":"linux","OperatingSystem":"Ubuntu","ServerVersion":"25.0.3"}`, valid + `{}`, `{"sentinel":"private"}`,
	} {
		if _, err := mappingFromInfo("docker", "/run/docker.sock", []byte(bad), unix.Ucred{}, 1000, maps); err != config.ErrSecretUnavailable {
			t.Fatalf("invalid Docker metadata: %v", err)
		}
	}
	for _, tc := range []struct {
		cred unix.Ucred
		maps Mapping
	}{
		{unix.Ucred{Uid: 1000}, maps}, {unix.Ucred{}, Mapping{}}, {unix.Ucred{}, Mapping{UID: []IDRange{{0, 100000, 65536}}, GID: maps.GID}},
	} {
		if _, err := mappingFromInfo("docker", "/run/docker.sock", []byte(valid), tc.cred, 1000, tc.maps); err != config.ErrSecretUnavailable {
			t.Fatal("unsupported namespace accepted")
		}
	}
}

const podmanRootlessInfo = `{"host":{"os":"linux","serviceIsRemote":false,"security":{"rootless":true},"remoteSocket":{"path":"unix:///run/user/1000/podman.sock","exists":true},"idMappings":{"uidmap":[{"container_id":0,"host_id":1000,"size":1},{"container_id":1,"host_id":100000,"size":65536}],"gidmap":[{"container_id":0,"host_id":1000,"size":1},{"container_id":1,"host_id":200000,"size":65536}]}},"version":{"Version":"5.0.0","Os":"linux"}}`

func TestMappingFromPodmanInfo(t *testing.T) {
	maps := Mapping{UID: []IDRange{{0, 1000, 1}, {1, 100000, 65536}}, GID: []IDRange{{0, 1000, 1}, {1, 200000, 65536}}}
	cred := unix.Ucred{Uid: 1000, Gid: 1000}
	socket := "/run/user/1000/podman.sock"
	if _, err := mappingFromInfo("podman", socket, []byte(podmanRootlessInfo), cred, 1000, maps); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(podmanRootlessInfo, `"serviceIsRemote":false`, `"serviceIsRemote":true`, 1),
		strings.Replace(podmanRootlessInfo, `"serviceIsRemote":false,`, ``, 1),
		strings.Replace(podmanRootlessInfo, `"rootless":true`, `"rootless":false`, 1),
		strings.Replace(podmanRootlessInfo, `"rootless":true`, `"rootless":null`, 1),
		strings.Replace(podmanRootlessInfo, `"exists":true`, `"exists":false`, 1),
		strings.Replace(podmanRootlessInfo, `unix:///run/user/1000/podman.sock`, `unix:///other`, 1),
		strings.Replace(podmanRootlessInfo, `"host_id":100000`, `"host_id":300000`, 1),
		strings.Replace(podmanRootlessInfo, `"host_id":200000`, `"host_id":1000`, 1),
		strings.Replace(podmanRootlessInfo, `"size":65536`, `"size":0`, 1),
		strings.Replace(podmanRootlessInfo, `"container_id":1`, `"container_id":0`, 1),
		strings.Replace(podmanRootlessInfo, `"container_id":1`, `"container_id":-1`, 1),
		strings.Replace(podmanRootlessInfo, `"os":"linux"`, `"os":"darwin"`, 1),
		strings.Replace(podmanRootlessInfo, `"Version":"5.0.0"`, `"Version":"5.1.0"`, 1),
		`{"host":null}`,
	} {
		if _, err := mappingFromInfo("podman", socket, []byte(bad), cred, 1000, maps); err != config.ErrSecretUnavailable {
			t.Fatalf("unsupported Podman metadata accepted: %v", err)
		}
	}
	for _, c := range []unix.Ucred{{Uid: 0}, {Uid: 1001}} {
		if _, err := mappingFromInfo("podman", socket, []byte(podmanRootlessInfo), c, 1000, maps); err != config.ErrSecretUnavailable {
			t.Fatal("unrelated/root peer accepted as rootless")
		}
	}
	// Actual rootful info omits idMappings rather than reporting rootless extents.
	rootful := `{"host":{"os":"linux","serviceIsRemote":false,"security":{"rootless":false},"remoteSocket":{"path":"/run/podman.sock","exists":true}},"version":{"Version":"5.0.0","Os":"linux"}}`
	identity := Mapping{UID: []IDRange{{0, 0, 4294967295}}, GID: []IDRange{{0, 0, 4294967295}}}
	if _, err := mappingFromInfo("podman", "/run/podman.sock", []byte(rootful), unix.Ucred{}, 1000, identity); err != nil {
		t.Fatal(err)
	}
}
func TestQualifiedPodmanLaunch(t *testing.T) {
	socket := "/run/user/1000/podman.sock"
	command := "podman\x00system\x00service\x00--time=0\x00unix://" + socket + "\x00"
	if !podmanCommand([]byte(command), socket) || !podmanConfig([]byte("[containers]\nuserns = \"host\"\n")) {
		t.Fatal("qualified launch rejected")
	}
	for _, bad := range []string{
		strings.Replace(command, "podman\x00", "podman\x00--module=unsafe\x00", 1),
		strings.Replace(command, "--time=0", "--time=1", 1), strings.Replace(command, "unix://", "tcp://", 1), command + "--userns=auto\x00", strings.TrimSuffix(command, "\x00"),
	} {
		if podmanCommand([]byte(bad), socket) {
			t.Fatal("ambiguous command accepted")
		}
	}
	for _, bad := range []string{"", `[containers]`, "[containers]\nuserns = \"auto\"", "[containers]\nuserns = \"keep-id\"", "[containers]\nuserns = \"\"", "[containers]\nuserns=\"host\"\nuserns_size=1000", "[containers]\nuserns=\"host\"\n[engine]\nenv=[\"PODMAN_USERNS=auto\"]", "[containers]\nuserns=\"host\"\nuserns=\"host\""} {
		if podmanConfig([]byte(bad)) {
			t.Fatal("ambiguous config accepted")
		}
	}
	valid := []byte("PATH=/usr/bin\x00CONTAINERS_CONF=/srv/alpha/containers.conf\x00HOME=/home/synthetic\x00")
	if p, err := podmanConfigPath(valid); err != nil || p != "/srv/alpha/containers.conf" {
		t.Fatal("qualified selector rejected")
	}
	for _, bad := range [][]byte{
		nil, []byte("CONTAINERS_CONF=relative\x00"), []byte("CONTAINERS_CONF=/srv/alpha/../conf\x00"),
		append(append([]byte{}, valid...), []byte("CONTAINERS_CONF_OVERRIDE=\x00")...),
		append(append([]byte{}, valid...), []byte("CONTAINERS_CONF_MODULES=sentinel\x00")...),
		append(append([]byte{}, valid...), []byte("PODMAN_USERNS=auto\x00")...),
		append(append([]byte{}, valid...), []byte("CONTAINER_HOST=unix:///other\x00")...),
		append(append([]byte{}, valid...), []byte("CONTAINERS_CONF=/other\x00")...), valid[:len(valid)-1],
	} {
		if _, err := podmanConfigPath(bad); err != config.ErrSecretUnavailable {
			t.Fatalf("ambiguous environment accepted: %v", err)
		}
	}
}
func TestRuntimeRejectsRemoteAndProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, socket := range []string{"", "unix:///run/docker.sock", "tcp://127.0.0.1:2375", "ssh://synthetic", "relative", "/run/../docker.sock"} {
		if _, err := runtimeMapping(ctx, "docker", socket); err != config.ErrSecretUnavailable {
			t.Fatal("noncanonical/remote socket accepted")
		}
	}
	dir := protectedTestDir(t)
	socket := filepath.Join(dir, "proxy.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan int, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- 0
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(time.Second))
		b := make([]byte, 16)
		n, _ := c.Read(b)
		done <- n
	}()
	if _, err := runtimeMapping(ctx, "podman", socket); err != config.ErrSecretUnavailable {
		t.Fatal("proxy/test process accepted as engine")
	}
	select {
	case n := <-done:
		if n != 0 {
			t.Fatal("unverified peer received HTTP request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy check did not terminate")
	}
}
func TestReadInfoBoundedAndFixed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		ok      bool
	}{
		{"valid", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"synthetic": "metadata"})
		}, true},
		{"failure", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "sentinel-private", 500) }, false},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://sentinel-private", 302) }, false},
		{"oversize", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 1024*1024+1)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// TCP is only a synthetic HTTP transport fixture; runtimeMapping never uses it.
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			srv := &http.Server{Handler: tc.handler}
			defer srv.Close()
			go srv.Serve(ln)
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = readInfo(ctx, conn, "/info")
			if (err == nil) != tc.ok || (err != nil && err != config.ErrSecretUnavailable) {
				t.Fatalf("fixed HTTP result: %v", err)
			}
		})
	}
}

func TestReadInfoCancellationAndHeaderLimit(t *testing.T) {
	for _, headers := range []bool{false, true} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if headers {
				w.Header().Set("Synthetic", strings.Repeat("x", 32769))
				w.WriteHeader(200)
				return
			}
			<-r.Context().Done()
		})}
		go srv.Serve(ln)
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err = readInfo(ctx, conn, "/info")
		cancel()
		conn.Close()
		srv.Close()
		if err != config.ErrSecretUnavailable {
			t.Fatalf("unbounded/canceled response accepted: %v", err)
		}
	}
}
