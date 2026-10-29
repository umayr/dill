#!/usr/bin/env bash
set -euo pipefail

instance="${LIMA_INSTANCE:-dill-podman}"
go_version="${GO_VERSION:-1.25.0}"
pkl_version="${PKL_VERSION:-0.31.1}"
podman_socket="${PODMAN_SOCKET:-unix:///tmp/dill-podman-${USER:-user}.sock}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v limactl >/dev/null 2>&1; then
  echo "lima is required. Install it with: brew install lima" >&2
  exit 1
fi

if ! limactl list "$instance" >/dev/null 2>&1; then
  limactl start --name "$instance" --tty=false template://ubuntu
fi

limactl shell "$instance" bash -lc "
set -euo pipefail

if ! command -v podman >/dev/null 2>&1; then
  sudo apt-get update
  sudo apt-get install -y podman curl ca-certificates make
fi

if ! command -v go >/dev/null 2>&1 || ! go version | grep -q 'go${go_version}'; then
  arch=\$(uname -m)
  case \"\$arch\" in
    x86_64) go_arch=amd64 ;;
    aarch64|arm64) go_arch=arm64 ;;
    *) echo \"unsupported architecture: \$arch\" >&2; exit 1 ;;
  esac
  curl -fsSL \"https://go.dev/dl/go${go_version}.linux-\${go_arch}.tar.gz\" -o /tmp/go.tgz
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf /tmp/go.tgz
fi

if ! command -v pkl >/dev/null 2>&1; then
  arch=\$(uname -m)
  case \"\$arch\" in
    x86_64) pkl_arch=amd64 ;;
    aarch64|arm64) pkl_arch=aarch64 ;;
    *) echo \"unsupported architecture: \$arch\" >&2; exit 1 ;;
  esac
  sudo curl -fsSL \"https://github.com/apple/pkl/releases/download/${pkl_version}/pkl-linux-\${pkl_arch}\" -o /usr/local/bin/pkl
  sudo chmod +x /usr/local/bin/pkl
fi

export PATH=/usr/local/go/bin:\$PATH
sock_path=\"${podman_socket#unix://}\"
rm -f \"\$sock_path\"
podman system service --time=0 \"${podman_socket}\" >/tmp/dill-podman-service.log 2>&1 &
for i in \$(seq 1 30); do
  [ -S \"\$sock_path\" ] && break
  sleep 1
done
[ -S \"\$sock_path\" ]

dump_podman_state() {
  podman version || true
  podman info || true
  podman ps -a || true
  podman images || true
  podman volume ls || true
  podman network ls || true
}

cd '$repo'
if ! PODMAN_SOCKET='${podman_socket}' DILL_TEST_ENGINE=podman DILL_TEST_TIMEOUT=\"\${DILL_TEST_TIMEOUT:-2m}\" make integration-test; then
  dump_podman_state >&2
  exit 1
fi
"
