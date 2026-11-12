#!/usr/bin/env bash
# Disposable qualification build; never a release artifact.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && $(uname -s) == Linux ]] || { echo 'Disposable Linux CI only' >&2; exit 1; }
readonly source_commit=e71ec6f1d94d2d97fb3afe08aae0d8adaf8bddf0 # upstream v5.0.0 tag target, pinned source identity
sudo apt-get update
sudo apt-get install -y gcc make pkg-config libseccomp-dev libsystemd-dev \
  podman uidmap slirp4netns passt crun fuse-overlayfs netavark aardvark-dns conmon catatonit
source_dir=$(mktemp -d "$RUNNER_TEMP/podman-alpha-source.XXXXXX")
trap 'rm -rf "$source_dir"' EXIT
git -C "$source_dir" init -q
git -C "$source_dir" remote add origin https://github.com/containers/podman.git
git -C "$source_dir" fetch --depth=1 origin "$source_commit"
git -C "$source_dir" checkout --detach FETCH_HEAD
test "$(git -C "$source_dir" rev-parse HEAD)" = "$source_commit"
# No opaque binaries. Source, vendored dependencies and build flags are pinned;
# native netavark/crun/conmon remain distro dependencies, recorded below.
GOTOOLCHAIN=go1.22.12 make -C "$source_dir" bin/podman \
  BUILDTAGS='containers_image_openpgp exclude_graphdriver_btrfs exclude_graphdriver_devicemapper seccomp systemd'
test "$("$source_dir/bin/podman" --version)" = 'podman version 5.0.0'
# Keep the distro executable path so Ubuntu's existing unprivileged-userns
# AppArmor profile applies. This replacement exists ONLY on the disposable job.
sudo install -o root -g root -m 0755 "$source_dir/bin/podman" /usr/bin/podman
printf 'Podman source commit: %s\n' "$source_commit"
/usr/bin/podman --version
crun --version
conmon --version
dpkg-query -W podman netavark aardvark-dns crun conmon
sha256sum /usr/bin/podman
