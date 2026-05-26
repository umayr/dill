#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$repo/scripts/integration-colima-docker.sh"
"$repo/scripts/integration-lima-podman.sh"
