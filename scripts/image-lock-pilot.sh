#!/usr/bin/env bash
# Run from the repository root on a disposable GitHub-hosted runner: bash scripts/image-lock-pilot.sh
set -Eeuo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted && ${RUNNER_OS:-} == Linux && ${RUNNER_ARCH:-} == X64 && $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Requires a GitHub-hosted Linux x86_64 runner' >&2; exit 1; }
[[ ${IMAGE_LOCK_PILOT_DISPOSABLE_DOCKER:-} == yes ]] || { echo 'Set IMAGE_LOCK_PILOT_DISPOSABLE_DOCKER=yes only for a disposable Docker daemon' >&2; exit 1; }
[[ -z ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} && -z ${DOCKER_TLS_VERIFY:-} && -z ${DOCKER_CERT_PATH:-} ]] || { echo 'Docker endpoint overrides are forbidden' >&2; exit 1; }
[[ $(docker context show) == default && $(docker context inspect default --format '{{.Endpoints.docker.Host}}') == unix:///var/run/docker.sock ]] || { echo 'Requires the local default Docker context' >&2; exit 1; }
[[ -x bin/dill && -f dill.pkl ]] || { echo 'Run from repo root with bin/dill built' >&2; exit 1; }
docker info >/dev/null
umask 077
tmp=$(mktemp -d "$PWD/.image-lock-pilot.XXXXXXXX")
id="pilot$(basename "$tmp" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
registry="${id}registry"
repo=""
a=""; b=""
cleanup() {
  set +e
  # The random project name is unique to this invocation; never prune globally.
  docker rm -f "${id}_web" "$registry" >/dev/null 2>&1
  docker network rm "$id" >/dev/null 2>&1
  [[ -z $repo ]] || docker image rm "$repo:latest" "$repo:a" "$repo:b" >/dev/null 2>&1
  [[ -z $a ]] || docker image rm "$a" >/dev/null 2>&1
  [[ -z $b ]] || docker image rm "$b" >/dev/null 2>&1
  rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
# Isolate registry credentials and Docker CLI configuration. The daemon remains the verified default.
export DOCKER_CONFIG="$tmp/docker-config"
mkdir -p "$DOCKER_CONFIG"
docker run -d --name "$registry" -p 127.0.0.1::5000 registry:2 >/dev/null
port=$(docker port "$registry" 5000/tcp)
[[ $port =~ ^127\.0\.0\.1:([0-9]+)$ ]] || { echo "Unexpected registry binding: $port" >&2; exit 1; }
repo="$port/$id"
for n in a b; do
  printf 'FROM busybox:1.36\nLABEL pilot.variant="%s"\nCMD ["sleep", "3600"]\n' "$n" > "$tmp/Dockerfile"
  docker build -t "$repo:$n" "$tmp"
done
a=$(docker image inspect "$repo:a" --format '{{.Id}}')
b=$(docker image inspect "$repo:b" --format '{{.Id}}')
[[ $a != "$b" ]]
docker tag "$repo:a" "$repo:latest"
# Retry only registry startup, not the assertions.
for attempt in {1..30}; do
  if docker push "$repo:latest"; then break; fi
  [[ $attempt -lt 30 ]] || exit 1
  sleep 1
done
cat > "$tmp/compose.pkl" <<EOF
amends "../dill.pkl"
config {
  name = "$id"
  engine = "docker"
  services {
    ["web"] {
      image = "$repo:latest"
    }
  }
}
EOF
bin/dill lock --file "$tmp/compose.pkl"
docker tag "$repo:b" "$repo:latest"
docker push "$repo:latest"
docker image rm "$repo:latest" "$repo:a" "$repo:b"
# Remove all aliases of the two synthetic image IDs, including any digest aliases.
docker image rm "$a" "$b" >/dev/null 2>&1 || true
check_image() {
  local expected=$1 containers cid actual
  containers=$(docker ps -q --filter "name=^/${id}_web$")
  [[ -n $containers && $(printf '%s\n' "$containers" | wc -l) -eq 1 ]] || { echo "Expected one running web container, got: $containers" >&2; exit 1; }
  cid=$containers
  actual=$(docker inspect --format '{{.Image}}' "$cid")
  [[ $actual == "$expected" ]] || { echo "Wrong container image: $actual (expected $expected)" >&2; exit 1; }
}
bin/dill up -f "$tmp/compose.pkl"
check_image "$a"
bin/dill lock --file "$tmp/compose.pkl" --update web
bin/dill up -f "$tmp/compose.pkl"
check_image "$b"
echo 'Image lock pilot passed: pinned A, then updated to B.'
