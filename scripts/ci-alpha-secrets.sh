#!/usr/bin/env bash
# This script owns only a disposable GitHub runner's dedicated Alpha daemon.
# Never point it at an existing operator/production daemon.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && $(uname -s) == Linux ]] || { echo 'Disposable Linux CI only' >&2; exit 1; }
mode=${1:?docker or podman-rootful or podman-rootless}
case "$mode" in docker|podman-rootful|podman-rootless) ;; *) exit 1 ;; esac
python3 - <<'PY'
import platform
v = tuple(int(x) for x in platform.release().split('-')[0].split('.')[:2])
assert v >= (6, 5), 'Alpha requires Linux >=6.5; no pidfd fallback'
print('Qualification kernel:', platform.release())
PY
base=/srv/dill-alpha-$mode
[[ ! -e $base ]] || { echo 'Dedicated fixture directory already exists' >&2; exit 1; }
operator=0; group=0
if [[ $mode == podman-rootless ]]; then operator=$(id -u); group=$(id -g); fi
sudo install -d -m 0700 -o "$operator" -g "$group" "$base" "$base/fixtures"
python3 scripts/install-pkl.py "$RUNNER_TEMP/alpha-pkl"
sudo install -m 0755 "$RUNNER_TEMP/alpha-pkl" "$base/pkl"
make build
repo=$PWD
CGO_ENABLED=0 go test -tags integration,containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs \
  -c -o "$RUNNER_TEMP/alpha-integration.test" ./test/integration
CGO_ENABLED=0 go test -tags containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs \
  -c -o "$RUNNER_TEMP/alpha-secretfile.test" ./internal/secretfile
CGO_ENABLED=0 go test -tags integration,containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs \
  -c -o "$RUNNER_TEMP/alpha-diagnostics.test" ./internal/orchestrator
socket=$base/engine.sock
pid=''
launcher=''
cleanup() {
  status=$?
  if [[ $status != 0 && -f $RUNNER_TEMP/alpha-daemon.log ]]; then
    python3 - "$RUNNER_TEMP/alpha-daemon.log" <<'PYDIAG'
import hashlib, sys
with open(sys.argv[1], 'rb') as f:
    f.seek(0, 2); f.seek(max(0, f.tell()-65536)); text = f.read(65536).decode('utf-8', 'replace')
blocked = ['dill-alpha-synthetic-v1', 'dill-alpha-synthetic-v2']
blocked += [hashlib.sha256(x.encode()).hexdigest() for x in blocked]
print('Dedicated synthetic Alpha daemon diagnostics (bounded):')
for line in text.splitlines():
    print('[synthetic source diagnostic withheld]' if any(x in line for x in blocked) else line)
PYDIAG
  fi
  # Stop only our exact recorded launch after the test has cleaned its containers.
  if [[ -n $pid ]]; then sudo kill "$pid" 2>/dev/null || true; elif [[ -n $launcher ]]; then sudo kill "$launcher" 2>/dev/null || true; fi
}
trap cleanup EXIT
if [[ $mode == docker ]]; then
  printf '{}' | sudo tee "$base/docker.json" >/dev/null
  sudo chmod 0600 "$base/docker.json"
  sudo env -i PATH=/usr/bin:/usr/local/bin:/bin HOME=/root \
    /usr/bin/dockerd --config-file "$base/docker.json" --host "unix://$socket" \
    --data-root "$base/data" --exec-root "$base/exec" --pidfile "$base/daemon.pid" \
    --bridge=none --iptables=false --ip6tables=false --ip-forward=false --ip-masq=false \
    >"$RUNNER_TEMP/alpha-daemon.log" 2>&1 &
  engine=docker
else
  test "$(/usr/bin/podman --version)" = 'podman version 5.0.0'
  printf '[containers]\nuserns = "host"\n' | sudo tee "$base/containers.conf" >/dev/null
  sudo chown "$operator:$group" "$base/containers.conf"
  sudo chmod 0600 "$base/containers.conf"
  if [[ $mode == podman-rootless ]]; then
    # Native rootless service, not podman unshare: Dill remains in the initial
    # host namespace and proves the exact daemon child namespace via pidfd.
    env -i PATH=/usr/bin:/usr/local/bin:/bin HOME="$HOME" USER="$(id -un)" LOGNAME="$(id -un)" \
      XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" \
      CONTAINERS_CONF="$base/containers.conf" \
      /usr/bin/podman system service --time=0 "unix://$socket" \
      >"$RUNNER_TEMP/alpha-daemon.log" 2>&1 &
  else
    sudo env -i PATH=/usr/bin:/usr/local/bin:/bin HOME=/root USER=root LOGNAME=root \
      CONTAINERS_CONF="$base/containers.conf" \
      /usr/bin/podman system service --time=0 "unix://$socket" \
      >"$RUNNER_TEMP/alpha-daemon.log" 2>&1 &
  fi
  engine=podman
fi
launcher=$!
# Query only the dedicated socket, through the trusted operator identity.
run_operator() { if [[ $operator == 0 ]]; then sudo "$@"; else "$@"; fi; }
for attempt in $(seq 1 90); do
  if run_operator test -S "$socket"; then break; fi
  kill -0 "$launcher" || { echo 'Dedicated daemon launch failed' >&2; exit 1; }
  sleep 1
done
run_operator test -S "$socket"
# Real SO_PEERPIDFD (Linux 6.5+), exact peer PID and kernel maps; no unshare guess
# and no socket proxy. These metadata-only facts contain no source contents.
run_operator python3 - "$socket" "$engine" "$mode" "$base/mapping.env" <<'PY'
import os, select, socket, struct, sys
path, engine, mode, output = sys.argv[1:]
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.connect(path)
pid, uid, gid = struct.unpack('3i', s.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
pidfd = s.getsockopt(socket.SOL_SOCKET, 77) # SO_PEERPIDFD, never pidfd_open(pid)
lifetime = select.poll(); lifetime.register(pidfd, select.POLLIN)
assert not lifetime.poll(0), "Exact peer exited before map observation"
assert os.path.basename(os.readlink(f'/proc/{pid}/exe')) == ('dockerd' if engine == 'docker' else 'podman')
def translate(which, container):
    rows = [tuple(map(int, line.split())) for line in open(f'/proc/{pid}/{which}_map')]
    matches = [host + container - inside for inside,host,size in rows if inside <= container < inside+size]
    assert len(matches) == 1
    return matches[0]
host_uid, host_gid = translate('uid', 1000), translate('gid', 1000)
if mode == 'podman-rootless':
    assert uid == os.geteuid() != 0 and host_uid != uid and host_uid != 1000
else:
    assert uid == 0 and (host_uid,host_gid) == (1000,1000)
assert not lifetime.poll(0), "Exact peer exited during map observation"
os.close(pidfd)
with open(output, 'x') as f:
    f.write(f'host_uid={host_uid}\nhost_gid={host_gid}\npid={pid}\n')
os.chmod(output, 0o600)
print('Exact peer pidfd and non-root UID/GID map qualified:', engine, mode)
PY
# Only this script's private generated numeric metadata is sourced.
eval "$(run_operator cat "$base/mapping.env")"
if [[ $engine == docker ]]; then
  sudo docker --host "unix://$socket" version --format '{{.Server.Version}}'
else
  run_operator /usr/bin/podman --url "unix://$socket" version --format '{{.Server.Version}}'
fi
common=(PATH=/usr/bin:/usr/local/bin:/bin DILL_TEST_ALPHA_SECRETS=1 DILL_TEST_SECRET_DIR="$base/fixtures" DILL_BINARY="$repo/bin/dill" \
  DILL_PKL_PATH="$base/pkl" DILL_ALPHA_DIAGNOSTIC_BINARY="$RUNNER_TEMP/alpha-diagnostics.test" DILL_ALPHA_ENGINE="$engine" DILL_ALPHA_SOCKET="$socket" \
  DILL_ALPHA_FIXTURE_ROOT="$base/fixtures" DILL_ALPHA_CONTAINER_UID=1000 DILL_ALPHA_CONTAINER_GID=1000 \
  DILL_ALPHA_HOST_UID="$host_uid" DILL_ALPHA_HOST_GID="$host_gid")
(cd "$repo/internal/secretfile"; run_operator env "${common[@]}" "$RUNNER_TEMP/alpha-secretfile.test" -test.v)
run_operator env "${common[@]}" "$RUNNER_TEMP/alpha-integration.test" \
  -test.run '^TestDillQualifiedHostFileSecrets$' -test.v
