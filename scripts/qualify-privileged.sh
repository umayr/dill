#!/usr/bin/env bash
# Only fresh GitHub-hosted runners. Synthetic container with no host mounts.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted && $(uname -s) == Linux ]]
engine=$1
case "$engine" in
  docker) socket=/var/run/docker.sock; cli=(docker --host "unix://$socket"); dill=(bin/dill) ;;
  podman) sudo systemctl start podman.socket; socket=/run/podman/podman.sock; cli=(sudo podman --remote --url "unix://$socket"); dill=(sudo env "DILL_PKL_PATH=$(command -v pkl)" "$PWD/bin/dill") ;;
  *) exit 2 ;;
esac
proj=$(mktemp -d); stack="dillpriv${GITHUB_RUN_ID}x${GITHUB_RUN_ATTEMPT}"; name="${stack}_probe"
trap '"${cli[@]}" rm -f "$name" >/dev/null 2>&1 || :; "${cli[@]}" network rm "$stack" >/dev/null 2>&1 || :; rm -rf "$proj"' EXIT
printf 'amends "%s/dill.pkl"\nconfig { name = "%s"; engine = "%s"; services { ["probe"] { image = "docker.io/library/busybox:1.36"; privileged = true; entrypoint = List("/bin/sleep"); command = List("300") } } }\n' "$PWD" "$stack" "$engine" > "$proj/compose.pkl"
args=(up -f "$proj/compose.pkl" --engine-socket "$socket")
if "${dill[@]}" "${args[@]}" >"$proj/denial" 2>&1; then exit 1; fi
grep -q 'privileged deployment requires' "$proj/denial"
if "${cli[@]}" container inspect "$name" >/dev/null 2>&1; then exit 1; fi
"${cli[@]}" pull docker.io/library/busybox:1.36
"${dill[@]}" "${args[@]}" --allow-privileged
[[ $("${cli[@]}" inspect --format '{{.HostConfig.Privileged}} {{.State.Running}}' "$name") == 'true true' ]]
