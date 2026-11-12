//go:build linux

package secretfile

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func runtimeMapping(ctx context.Context, engine, socket string) (*runtimeEvidence, error) {
	fail := func() (*runtimeEvidence, error) { return nil, config.ErrSecretUnavailable }
	if engine != "docker" && engine != "podman" {
		return fail()
	}
	// Read proc maps from the initial host user namespace, not inside a container
	// or an operator's unshare session (where "host" IDs would be misleading).
	self, err := unix.Open("/proc/self", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	defer unix.Close(self)
	uid, err := procMap(self, "uid_map")
	if err != nil || !identityMap(uid) {
		return fail()
	}
	gid, err := procMap(self, "gid_map")
	if err != nil || !identityMap(gid) {
		return fail()
	}
	stat, err := readProc(self, "stat")
	if err != nil {
		return fail()
	}
	if _, ok := processStart(stat, int32(os.Getpid())); !ok {
		return fail()
	}
	parent, name, err := openParent(socket, uint32(os.Geteuid()))
	if err != nil {
		return fail()
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Nlink != 1 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0002 != 0 {
		return fail()
	}
	// Connecting through the pinned O_PATH descriptor names this exact socket
	// inode; do not independently re-resolve the policy path in an SDK pool.
	d := net.Dialer{}
	c, err := d.DialContext(ctx, "unix", "/proc/self/fd/"+strconv.Itoa(fd))
	if err != nil {
		return fail()
	}
	defer c.Close()
	conn, ok := c.(*net.UnixConn)
	if !ok {
		return fail()
	}
	p, err := capturePeer(conn, engine)
	if err != nil {
		return fail()
	}
	defer p.close()
	if p.cred.Uid != st.Uid {
		return fail()
	}
	if engine == "podman" && p.qualifiedPodman(socket) != nil {
		return fail()
	}
	endpoint := "/info"
	if engine == "podman" {
		endpoint = "/v5.0.0/libpod/info"
	}
	info, err := readInfo(ctx, conn, endpoint)
	if err != nil {
		return fail()
	}
	mapping, err := mappingFromInfo(engine, socket, info, p.cred, uint32(os.Geteuid()), p.snapshot.mapping)
	if err != nil {
		return fail()
	}
	after, err := p.inspect(engine)
	if err != nil || !sameIdentity(p.snapshot, after) {
		return fail()
	}
	// Recheck the dedicated service launch and config after the request as well.
	if engine == "podman" && p.qualifiedPodman(socket) != nil {
		return fail()
	}
	return &runtimeEvidence{mapping: mapping, pid: p.cred.Pid, start: p.snapshot.start, executable: p.snapshot.executable, userNS: p.snapshot.userNS}, nil
}

func readInfo(ctx context.Context, conn net.Conn, endpoint string) ([]byte, error) {
	used := false
	tr := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32768,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if used {
				return nil, config.ErrSecretUnavailable
			}
			used = true
			return conn, nil
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+endpoint, nil)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, config.ErrSecretUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(b) > 1024*1024 {
		return nil, config.ErrSecretUnavailable
	}
	return b, nil
}

// These are the actual pinned SDK wire fields: Docker api/types/system.Info;
// Podman libpod/define.Info and containers/storage/pkg/idtools.IDMap. Pointer
// booleans distinguish missing namespace evidence from a genuine false value.
func mappingFromInfo(engine, socket string, b []byte, cred unix.Ucred, operator uint32, maps Mapping) (Mapping, error) {
	fail := func() (Mapping, error) { return Mapping{}, config.ErrSecretUnavailable }
	if engine == "docker" {
		var info struct {
			OSType, OperatingSystem, ServerVersion string
			SecurityOptions                        []string
		}
		if json.Unmarshal(b, &info) != nil || info.OSType != "linux" || info.ServerVersion == "" || info.OperatingSystem == "" || strings.Contains(strings.ToLower(info.OperatingSystem), "desktop") || info.SecurityOptions == nil || cred.Uid != 0 || !identityMap(maps.UID) || !identityMap(maps.GID) {
			return fail()
		}
		for _, option := range info.SecurityOptions {
			name := strings.SplitN(option, ",", 2)[0]
			if name == "name=rootless" || name == "name=userns" {
				return fail()
			}
		}
		return maps, nil
	}
	if engine != "podman" {
		return fail()
	}
	type wireRange struct {
		Container int64 `json:"container_id"`
		Host      int64 `json:"host_id"`
		Size      int64 `json:"size"`
	}
	var info struct {
		Host *struct {
			OS              string `json:"os"`
			ServiceIsRemote *bool  `json:"serviceIsRemote"`
			Security        *struct {
				Rootless *bool `json:"rootless"`
			} `json:"security"`
			RemoteSocket *struct {
				Path   string `json:"path"`
				Exists bool   `json:"exists"`
			} `json:"remoteSocket"`
			IDMappings struct {
				UID []wireRange `json:"uidmap"`
				GID []wireRange `json:"gidmap"`
			} `json:"idMappings"`
		} `json:"host"`
		Version struct{ Version, Os string } `json:"version"`
	}
	if json.Unmarshal(b, &info) != nil || info.Host == nil || info.Host.OS != "linux" || info.Version.Version != "5.0.0" || info.Version.Os != "linux" {
		return fail()
	}
	h := info.Host
	if h.ServiceIsRemote == nil || *h.ServiceIsRemote || h.Security == nil || h.Security.Rootless == nil || h.RemoteSocket == nil || !h.RemoteSocket.Exists || (h.RemoteSocket.Path != socket && h.RemoteSocket.Path != "unix://"+socket) {
		return fail()
	}
	if !*h.Security.Rootless {
		if cred.Uid != 0 || !identityMap(maps.UID) || !identityMap(maps.GID) || len(h.IDMappings.UID) != 0 || len(h.IDMappings.GID) != 0 {
			return fail()
		}
		return maps, nil
	}
	if cred.Uid == 0 || cred.Uid != operator {
		return fail()
	}
	convert := func(ranges []wireRange) ([]IDRange, error) {
		result := make([]IDRange, 0, len(ranges))
		for _, r := range ranges {
			if r.Container < 0 || r.Host < 0 || r.Size <= 0 {
				return nil, config.ErrSecretUnavailable
			}
			result = append(result, IDRange{uint64(r.Container), uint64(r.Host), uint64(r.Size)})
		}
		if !validRanges(result) {
			return nil, config.ErrSecretUnavailable
		}
		return result, nil
	}
	u, err := convert(h.IDMappings.UID)
	if err != nil {
		return fail()
	}
	g, err := convert(h.IDMappings.GID)
	if err != nil || !sameRanges(u, maps.UID) || !sameRanges(g, maps.GID) {
		return fail()
	}
	return maps, nil
}

func (p *peer) qualifiedPodman(socket string) error {
	argv, err := readProc(p.proc, "cmdline")
	if err != nil {
		return config.ErrSecretUnavailable
	}
	defer clear(argv)
	if !podmanCommand(argv, socket) {
		return config.ErrSecretUnavailable
	}
	env, err := readProc(p.proc, "environ")
	if err != nil {
		return config.ErrSecretUnavailable
	}
	defer clear(env)
	conf, err := podmanConfigPath(env)
	if err != nil {
		return config.ErrSecretUnavailable
	}
	b, err := readPrivate(conf, p.cred.Uid)
	if err != nil || !podmanConfig(b) {
		return config.ErrSecretUnavailable
	}
	return nil
}

func podmanCommand(b []byte, socket string) bool {
	args := bytes.Split(b, []byte{0})
	// A dedicated non-socket-activated service, no global flags/modules/remote,
	// no per-service config selectors. argv[0] must name the verified executable.
	return len(args) == 6 && len(args[5]) == 0 && (string(args[0]) == "podman" || strings.HasSuffix(string(args[0]), "/podman")) && string(args[1]) == "system" && string(args[2]) == "service" && string(args[3]) == "--time=0" && string(args[4]) == "unix://"+socket
}
func podmanConfigPath(b []byte) (string, error) {
	var conf string
	seen := map[string]bool{}
	if len(b) == 0 || b[len(b)-1] != 0 {
		return "", config.ErrSecretUnavailable
	}
	for _, field := range bytes.Split(b[:len(b)-1], []byte{0}) {
		key, value, ok := bytes.Cut(field, []byte{'='})
		if !ok {
			return "", config.ErrSecretUnavailable
		}
		k := string(key)
		if seen[k] {
			return "", config.ErrSecretUnavailable
		}
		seen[k] = true
		switch {
		case k == "CONTAINERS_CONF":
			conf = string(value)
		case strings.HasPrefix(k, "CONTAINERS_CONF"), k == "PODMAN_USERNS", k == "CONTAINER_HOST", k == "CONTAINER_CONNECTION":
			// Even an empty override is rejected: this is a narrow qualified launch,
			// not a generic containers.conf/environment evaluator.
			return "", config.ErrSecretUnavailable
		}
	}
	if !cleanPath(conf) {
		return "", config.ErrSecretUnavailable
	}
	return conf, nil
}
func podmanConfig(b []byte) bool {
	var c struct {
		Containers struct {
			UserNS string `toml:"userns"`
		} `toml:"containers"`
	}
	meta, err := toml.Decode(string(b), &c)
	if err != nil || len(meta.Undecoded()) != 0 || c.Containers.UserNS != "host" {
		return false
	}
	// Only the containers table and its userns setting; no other layering,
	// environment rewriting, idmapping or defaults are supported by Alpha.
	for _, k := range meta.Keys() {
		if k.String() != "containers" && k.String() != "containers.userns" {
			return false
		}
	}
	return true
}
