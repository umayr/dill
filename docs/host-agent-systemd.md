# Install the combined Dill agent on private HTTPS (Linux/systemd)

This example is for a **clean install** with the combined `dill agent` command, a systemd-managed rootful Docker daemon on the same Linux host, and a trusted Brine instance. The [example unit](../packaging/systemd/dill-agent.service) serves only session, inventory, inspection and logs. It does not enable environment reveal, staging, preview, or deployment. This is an experimental example, not a qualified installation on any particular Linux distribution. Do not use these steps to overwrite an existing agent installation or its state.

## 1. Choose the access boundary

- Prefer `127.0.0.1:8443` if Brine can connect from the agent's host network namespace. Otherwise, use a literal private IP assigned to this host, allow the port only from the intended client(s) at the host firewall, and do not publish or forward it to the internet. A private IP is not a firewall. Keep TLS between Brine and the agent; Brine pins the agent's certificate.
- Access to the rootful Docker socket is effectively host-root-equivalent, even when the agent only exposes read-only routes. Review the service account, the Docker socket and its ancestors (including ACLs), and any group membership before granting access. Stop if this trust is unacceptable. Never make the Docker socket world-accessible or expose it over the network. Logs and inspection can contain sensitive data.
- Have an authorized Linux administrator run the provisioning steps. Commands below assume Bash, GNU user/account tools, `sudo`, `curl`, Python 3, and a systemd version supporting the unit's directives.

## 2. Get the released Linux binary and matching examples

1. On the target host, run `uname -m`. From the official [Dill releases](https://github.com/umayr/dill/releases), choose a specific tag that supports `dill agent` with HTTPS. Download `dill-linux-amd64` for `x86_64` or `dill-linux-arm64` for `aarch64` (`arm64`). These are the combined executable; do not install `dill-agent` or `dill-serve` in its place.
2. Before executing the download, compute its SHA-256 with `sha256sum` and compare it to a trusted expected digest for that exact tag and asset. GitHub's release API (`https://api.github.com/repos/umayr/dill/releases/tags/<tag>`) exposes an asset `digest` when available. This checks the bytes against GitHub's metadata; it is not an independent publisher signature. Older releases may lack a binary digest. The Pkl schema `.sha256` files do not cover these executables. Without a trusted expected digest, do not treat the download as verified; use the source fallback if your policy requires verification. Do not use a download with a digest mismatch.
3. Obtain `packaging/systemd/dill-agent.service` and `packaging/systemd/agent.env.example` from the **same tag's source tree**, using the release's source archive or a checkout of the tag. They are source files, not separate binary assets. Older tags may support HTTPS but predate the examples. For such a tag, either review both examples from one pinned revision for compatibility with the tagged agent before using them, or use the source fallback. Do not copy templates from a moving branch and assume they match an older binary.

**Source fallback:** On a trusted Linux build host for the target architecture, check out a reviewed revision that contains both examples and run `make build` with the Go toolchain required by `go.mod`. Use `bin/dill` and the templates from that same revision.

## 3. Install the binary and provision the identity

Work from the reviewed source tree containing the examples. Set `dill_binary` to the absolute path of the selected release asset (change the example path and architecture as needed), or to the absolute path of `bin/dill` for a source build. Run the version and help checks as an unprivileged user; check the release version against the tag and confirm the help shows `--https-dir`, `--https-listen`, `--print-https-pairing`, the engine enrollment flags, and `init-https --dir`. Stop if the binary does not match the selected version or support the documented commands.

```sh
dill_binary=/absolute/path/to/dill-linux-amd64
chmod u+x "$dill_binary"
"$dill_binary" version
"$dill_binary" agent --help
"$dill_binary" agent init-https --help
```

In the same shell, confirm the account/group and destination files/directories don't already exist. Check that the binary and configuration paths have trusted ancestors. Adapt the `nologin` path if your distribution uses another location.

```sh
sudo install -o root -g root -m 0755 "$dill_binary" /usr/local/bin/dill
sudo groupadd --system dillagent
sudo useradd --system --gid dillagent --home-dir /var/lib/dill-agent \
  --shell /usr/sbin/nologin dillagent
sudo install -d -o root -g root -m 0750 /etc/dill-agent
sudo install -d -o dillagent -g dillagent -m 0700 /var/lib/dill-agent
```

If the reviewed socket is controlled by the `docker` group and host-root-equivalent access was approved, grant that access explicitly: `sudo usermod --append --groups docker dillagent`. The example unit does not grant it. Confirm the chosen socket is a local Docker endpoint. As the service account, query its actual daemon ID (adjust `/run/docker.sock` if required):

```bash
(
  set -o pipefail
  sudo -u dillagent -- curl --fail --silent --show-error --max-time 5 \
    --noproxy '*' --unix-socket /run/docker.sock http://localhost/info |
    python3 -c 'import json,sys; v=json.load(sys.stdin)["ID"]; assert isinstance(v,str) and v; print(v)'
)
```

Use the observed ID only if the command succeeds. Generate an enrollment UUID **once** for this new target (`python3 -c 'import uuid; print(uuid.uuid4())'`). The lowercase UUID is not the Docker daemon ID. Start with generation `1` for new enrollment. Record the UUID, daemon ID, exact socket path and generation, and reuse them on each start.

Generate the agent credentials **once**, as the service user. The `https` child must not exist; do not create it in advance:

```sh
sudo -u dillagent -- /usr/local/bin/dill agent init-https --dir /var/lib/dill-agent/https
```

Confirm that the directory is owned by `dillagent` with mode `0700`, and that `tls-cert.pem`, `tls-key.pem`, `token`, and `session-seed` are regular files owned by `dillagent` with mode `0600`. Check ACLs and ancestors; do not print file contents. If initialization fails, investigate any partial directory; do not overwrite or blindly reuse it. Securely back up the credential directory and enrollment settings according to local policy. Never put the credential files' contents in the unit, its environment file, logs, or reports.

## 4. Configure and start

```sh
sudo install -o root -g root -m 0600 packaging/systemd/agent.env.example /etc/dill-agent/agent.env
sudoedit /etc/dill-agent/agent.env
sudo install -o root -g root -m 0644 packaging/systemd/dill-agent.service /etc/systemd/system/dill-agent.service
sudo systemd-analyze verify /etc/systemd/system/dill-agent.service
```

Replace both `REPLACE_WITH_…` values before proceeding. Set the chosen literal listener IP and port, and the exact absolute Docker socket path. Do not put credentials in `agent.env`. Check the firewall before enabling a non-loopback listener. Proceed only if the verification succeeds:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now dill-agent.service
sudo systemctl is-active dill-agent.service
```

If the unit fails to start, inspect `sudo journalctl -u dill-agent.service -b -n 50 --no-pager` locally for bind, path or permission issues. Do not copy sensitive output into reports. Systemd ordering is not a readiness check: the listener can be active while Docker is unavailable or the daemon ID does not match.

## 5. Pair and verify

In a private local terminal with no recording or shared scrollback, run the following as `dillagent`, replacing the placeholder arguments with the **exact** non-secret settings in `agent.env` (including the generation if different). This exits without opening a listener or checking the live daemon. **Stdout is a reusable secret**. Do not run it through systemd, paste it into chat, or redirect it to a regular file.

```sh
sudo -u dillagent -- /usr/local/bin/dill agent \
  --https-dir /var/lib/dill-agent/https \
  --https-listen 'LISTEN_IP:PORT' \
  --engine-socket '/run/docker.sock' \
  --engine-id 'ENROLLED_ENGINE_UUID' \
  --daemon-id 'OBSERVED_DOCKER_ID' \
  --generation 1 --print-https-pairing
```

Transfer the bundle only through an approved secret-handling channel. In Brine, use that bundle and the corresponding `https://IP:PORT` endpoint. Verify a fresh authenticated session proof against the enrolled binding and perform an inventory read. This exercises the pinned TLS connection and the agent's check of the actual Docker daemon; a running unit or a successful pairing export alone proves neither. Do not bypass TLS verification or put the bearer token on a command line.

## 6. Restart without re-enrolling

On an ordinary restart, leave `/var/lib/dill-agent/https` and the enrollment values unchanged. Do not rerun initialization or export a new pairing bundle. Confirm Docker and the configured IP are available. If you changed the unit, verify it again and run `sudo systemctl daemon-reload` first.

```sh
sudo systemctl restart dill-agent.service
sudo systemctl is-active dill-agent.service
```

Repeat the fresh session and inventory check with the existing Brine enrollment. A fresh process has a new session epoch; the TLS pin, signing identity and engine binding should be unchanged. Investigate a daemon ID or pin mismatch rather than accepting it automatically. Coordinate certificate renewal, credential rotation or any binding change separately; removing a target from Brine does not revoke a copied token.

See [Direct HTTPS host agent](host-agent-https.md) for separate feature opt-ins. This service example does not provision writable snapshot, coordination, or history directories. If you operate a separate write-enabled deployment, preserve its durable state across restarts; an admitted operation may become `unknown` and is not automatically replayed.
