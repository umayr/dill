# Proposed manual Linux host-agent provisioning

**Status: proposal, not a generally qualified installation procedure.** The exact commands below have not been qualified as a complete procedure. An adapted installation was exercised on two disposable Colima Linux ARM64 VMs with Docker 29.5.2 and Ubuntu OpenSSH 9.6p1: systemd start/restart, real SSH streamlocal forwarding, and a Brine read-only browser flow. That experiment required the separate [Brine pinned-host-key negotiation correction](https://github.com/umayr/brine/pull/90) and explicit approval of broader local TCP forwarding, as explained below. It does not qualify production security, arbitrary hosts, rootless Docker, Podman, or the literal command examples. This document does not install or start anything and contains no stack lifecycle operations. `dill-agent` is experimental, Docker-only, and does not authenticate socket clients; inspection and logs may contain secrets. See `host-agent.md` for protocol limitations. Do not substitute `dill-serve`.

## Security decision before provisioning

Use a dedicated Linux service identity, e.g. `dillagent`, and an explicitly selected **local** Docker Unix socket. Provisioning users, package installation, engine access and systemd configuration requires an appropriately authorized administrator; none requires assuming the operator is root. Rootless Docker may be an option if its socket is accessible to this identity, but is untested. Access to a rootful Docker socket, including through a `docker` group or ACL, is effectively host-root-equivalent and permits mutation despite the agent's read-only API. Access to a rootless engine grants broad control over that user's containers and data. Review all group memberships, ACLs and every path ancestor; mode bits alone are not a complete access audit. A socket proxy is not proof of a local engine.

**SSH limitation / blocker:** the agent requires its socket's immediate parent to be owned by the agent UID with no group/other mode bits, and sets the socket to 0600. A distinct SSH UID cannot normally traverse that directory or connect. POSIX ACL grants can affect the mode mask checked by the agent; do not assume an ACL bypass works or is safe. Using the *same* UID for SSH permits that account to connect to the engine socket as well as the agent socket. OpenSSH streamlocal forwarding has no demonstrated per-destination restriction here; `PermitOpen` is for TCP destinations, not a restriction on Unix socket paths. Thus the example below is only acceptable if the SSH credential is trusted with **all** resources accessible to the agent UID, including engine-level privileges. It is not a least-privilege, agent-socket-only solution. If that trust is unacceptable, stop: a separately designed and tested access boundary is required. Do not weaken socket permissions or claim ACLs solve this.

## Executable and identity

On a trusted Linux build host, with the Go version required by `go.mod`, build the exact reviewed revision (the tags match the repository Makefile):

```sh
go build -tags containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs -o ./dill-agent ./cmd/dill-agent
./dill-agent -h
```

Have an administrator install that reviewed executable as `/usr/local/bin/dill-agent`, root-owned and not writable by the service account or other untrusted users, and protect its ancestors. Build dependencies and Go toolchain must also be trusted. This Linux build/install is **untested**; a local macOS build and help invocation succeeded.

Provision `/var/lib/dill-agent` and `/run/dill-agent` as `dillagent`-owned directories mode 0700, with trusted ancestors and no unintended ACLs. The former holds persistent identity; the latter holds the socket. The agent refuses an existing socket path, including a stale one. Investigate any stale path and arrange a separately reviewed safe recovery procedure; this proposal does not delete it. Do not put secrets in unit arguments, logs, shell history, or reports.

The agent needs a 32-byte Ed25519 **seed in hex**, not an OpenSSH private key. The following standard-library Go helper exclusively creates the seed file, prints only the public key and its agent fingerprint, and can later inspect the same file. Run it as `dillagent` in a trusted directory after independently reviewing it; do not share the seed. The public key is unpadded base64url; `agentId` is lowercase hex SHA-256 of the raw 32-byte public key, as implemented by the agent (not an SSH fingerprint). Preserve and enroll these values out of band.

```sh
umask 077
# Create this source in a private trusted working directory.
cat > agent-identity.go <<'GO'
package main
import (
 "crypto/ed25519"
 "crypto/rand"
 "crypto/sha256"
 "encoding/base64"
 "encoding/hex"
 "fmt"
 "os"
 "strings"
)
func main() {
 if len(os.Args) != 3 { panic("usage: agent-identity create|inspect seed-file") }
 path := os.Args[2]
 if os.Args[1] == "create" {
  seed := make([]byte, ed25519.SeedSize)
  if _, err := rand.Read(seed); err != nil { panic(err) }
  f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); if err != nil { panic(err) }
  if _, err = fmt.Fprintln(f, hex.EncodeToString(seed)); err != nil { panic(err) }
  if err = f.Close(); err != nil { panic(err) }
 } else if os.Args[1] != "inspect" { panic("unknown operation") }
 b, err := os.ReadFile(path); if err != nil { panic(err) }
 seed, err := hex.DecodeString(strings.TrimSpace(string(b))); if err != nil || len(seed) != ed25519.SeedSize { panic("invalid seed") }
 pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
 id := sha256.Sum256(pub)
 fmt.Printf("agentPublicKey=%s\nagentId=%x\n", base64.RawURLEncoding.EncodeToString(pub), id)
}
GO
go run agent-identity.go create /var/lib/dill-agent/seed
# Later, inspect the public identity without printing the seed:
go run agent-identity.go inspect /var/lib/dill-agent/seed
```

Use a private trusted working directory and inspect ownership, mode, ACLs and ancestors of the seed separately. `create` refuses an existing file; preserve the existing seed across restarts, securely back it up according to local policy, and never reuse it for SSH. A failed write may leave an incomplete file; investigate rather than overwrite. Go compilation may create cache files; secure the build environment. These exact commands are intended for Linux but only the helper logic was checked locally with a synthetic temporary seed.

Generate the **enrollment engine UUID once**, record it durably, and reuse it, rather than generating it on each start (Python 3 required):

```sh
python3 -c 'import uuid; print(uuid.uuid4())'
```

This is an operator-assigned lowercase UUID, **not** the Docker daemon ID. Independently obtain the actual daemon's stable `ID` from the chosen socket's Docker `/info` response, for example (curl and Python 3 required; replace the path after independently qualifying it):

```sh
curl --fail --silent --show-error --unix-socket /var/run/docker.sock http://localhost/info | python3 -c 'import json,sys; v=json.load(sys.stdin)["ID"]; assert isinstance(v,str) and v; print(v)'
```

This prints only the ID, not the full potentially sensitive response. The pipeline must be checked for errors; use a shell with `set -o pipefail`. Do not invent or randomly generate the daemon ID: record the observed value and independently verify the endpoint. Record and independently enroll the complete signed binding: agent public key and `agentId`, engine UUID, nonzero generation, `engineKind` (`docker`), exact `engineSocket` (`unix://` followed by the configured absolute engine socket path), and observed daemon ID. A path change is a binding change even if the daemon is unchanged; re-enroll deliberately on any identity or binding change. Coordinate generation (initially `1`) with the verifier rather than generating it on each restart. The agent rechecks daemon ID; it is not cryptographic attestation. The `/info` command was **not tested against Docker**.

## Proposed systemd service (untested)

After provisioning the account, engine access, binary, seed, and enrollment, an administrator can adapt the following. Replace UUID and daemon ID with the recorded values; ensure neither contains systemd syntax or whitespace before placing them in the unit. Select the actual engine socket. `RuntimeDirectory` creates the private runtime directory for the service; confirm ownership, ACLs, and ancestor safety on the target. No supplementary engine group is assumed: configure access only after the security decision above.

```ini
[Unit]
Description=Experimental Dill read-only host agent
After=network.target

[Service]
User=dillagent
Group=dillagent
RuntimeDirectory=dill-agent
RuntimeDirectoryMode=0700
UMask=0077
ExecStart=/usr/local/bin/dill-agent --socket /run/dill-agent/agent.sock --engine-socket /var/run/docker.sock --engine-id REPLACE-WITH-LOWERCASE-UUID --daemon-id REPLACE-WITH-OBSERVED-ID --generation 1 --key-file /var/lib/dill-agent/seed
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

Do not configure `--reveal-public-key` unless separately authorized and enrolled; reveal is disabled without it. A running systemd service does not prove Docker is available or that its identity matches: the agent checks the engine at request time. `After=network.target` does not establish engine readiness; target-specific engine ordering and readiness are untested. Unit parsing, runtime-directory behavior, hardening compatibility, restart and stale-socket recovery also remain untested. No service was installed or started.

## SSH streamlocal forwarding: conditional example, not qualified

Only if the engine-level trust described above **and local TCP destination access from the target** are accepted, a dedicated SSH login using the **same UID** could forward the agent socket. This is not an agent-socket-only credential.

**Observed compatibility limitation:** on Ubuntu OpenSSH 9.6p1 (Ubuntu-3ubuntu13.16), `AllowTcpForwarding no` together with `AllowStreamLocalForwarding local` denied the Unix-socket channel despite correct same-UID socket access. The OpenSSH permission tables used by streamlocal connection checks are also disabled by the TCP forwarding setting. The disposable experiment worked with `AllowTcpForwarding local`; that enables local TCP forwarding too, not just Unix sockets. Do not silently loosen an existing SSH policy, and do not treat `PermitOpen` as a demonstrated per-Unix-socket restriction. If this broader access is unacceptable, stop and design a separately tested boundary.

Have an SSH administrator evaluate a dedicated `Match User dillagent` block along these lines only after that decision, ensuring global configuration does not override it and authentication/authorized keys are root-controlled or otherwise protected from that UID:

```text
Match User dillagent
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    AllowTcpForwarding local
    AllowStreamLocalForwarding local
    PermitTunnel no
    PermitUserRC no
    ForceCommand /bin/false
```

`local` means client-initiated local forwarding to a server-side Unix socket; `ForceCommand` prevents a useful shell but does not prevent forwarding with `ssh -N`. Restrict who can authenticate and protect the authorized key and host keys; verify the actual effective `sshd` configuration and account policy (including whether login is permitted) on the target. These settings do **not** confine destinations or make engine access read-only. The adapted disposable profile exercised this behavior on the version identified above; other SSH versions, the exact example account setup and deployment-specific policy remain unqualified. Confirm the server host key through an independent trusted channel and configure strict host-key checking before connecting. For a client-owned private directory (0700) and a non-existing local socket path, the intended shape is:

```sh
ssh -N -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=yes \
  -L /absolute/private/client/agent.sock:/run/dill-agent/agent.sock dillagent@HOST
```

Use a dedicated protected SSH key, an independently verified known-hosts entry, and client filesystem/ACL controls. The command opens an ongoing tunnel; it has **not** been run. `ExitOnForwardFailure=yes` checks establishment of the local listener, not successful connection to the remote socket, agent availability, or a valid session. The adapted disposable experiment exercised real SSH, session-proof verification and end-to-end enrollment with the separate Brine fix; this literal tunnel command and a general receiving-host procedure remain unqualified. A tunnel alone does not verify the agent's signed identity: a client must independently verify the session proof against the enrolled public key and binding.
