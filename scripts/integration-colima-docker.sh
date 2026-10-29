#!/usr/bin/env bash
set -euo pipefail

profile="${COLIMA_PROFILE:-dill-docker}"
cpu="${COLIMA_CPU:-2}"
memory="${COLIMA_MEMORY:-4}"
disk="${COLIMA_DISK:-20}"
export DILL_TEST_ENGINE="${DILL_TEST_ENGINE:-docker}"
export DILL_TEST_TIMEOUT="${DILL_TEST_TIMEOUT:-2m}"

if ! command -v colima >/dev/null 2>&1; then
  echo "colima is required. Install it with: brew install colima docker" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker CLI is required. Install it with: brew install docker" >&2
  exit 1
fi

if ! colima status --profile "$profile" >/dev/null 2>&1; then
  colima start \
    --profile "$profile" \
    --runtime docker \
    --cpu "$cpu" \
    --memory "$memory" \
    --disk "$disk"
fi

eval "$(colima docker-env --profile "$profile" --shell bash)"
docker version

dump_docker_state() {
  docker version || true
  docker info || true
  docker ps -a || true
  docker images || true
  docker volume ls || true
  docker network ls || true
}

if ! make integration-test; then
  dump_docker_state >&2
  exit 1
fi
