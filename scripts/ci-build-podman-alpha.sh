#!/usr/bin/env bash
# Disposable qualification build; never a release artifact.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && $(uname -s) == Linux ]] || { echo 'Disposable Linux CI only' >&2; exit 1; }
readonly crun_version=1.26
readonly crun_source_commit=3241e671f92c33b0c003cd7de319e4f32add6231 # upstream 1.26 tag target
readonly source_commit=e71ec6f1d94d2d97fb3afe08aae0d8adaf8bddf0 # upstream v5.0.0 tag target, pinned source identity
sudo apt-get update
sudo apt-get install -y gcc make pkg-config libseccomp-dev libsystemd-dev \
  autoconf automake libtool libcap-dev libyajl-dev python3 iptables iproute2 \
  podman uidmap slirp4netns passt crun fuse-overlayfs netavark aardvark-dns conmon catatonit
source_dir=$(mktemp -d "$RUNNER_TEMP/podman-alpha-source.XXXXXX")
crun_source_dir=$(mktemp -d "$RUNNER_TEMP/crun-alpha-source.XXXXXX")
trap 'rm -rf "$source_dir" "$crun_source_dir"' EXIT
# Podman emits OCI 1.2.0. Distro crun 1.14.1 hard-rejects versions not
# containing "1.0"; replace only this disposable job's runtime, not the spec.
git -C "$crun_source_dir" init -q
git -C "$crun_source_dir" remote add origin https://github.com/containers/crun.git
git -C "$crun_source_dir" fetch --depth=1 origin "tag" "$crun_version"
git -C "$crun_source_dir" checkout --detach "$crun_version"
test "$(git -C "$crun_source_dir" rev-parse HEAD)" = "$crun_source_commit"
git -C "$crun_source_dir" submodule update --init --recursive
# Recursive submodules use the superproject's recorded gitlinks, never --remote.
git -C "$crun_source_dir" submodule status --recursive
if git -C "$crun_source_dir" submodule status --recursive | grep -Eq '^[-+U]'; then
  echo 'Unpinned crun submodule checkout' >&2; exit 1
fi
(
  cd "$crun_source_dir"
  ./autogen.sh
  ./configure --prefix=/usr --disable-shared --enable-seccomp --enable-systemd
  make -j2
)
test "$("$crun_source_dir/crun" --version | sed -n '1p')" = "crun version $crun_version"
sudo install -o root -g root -m 0755 "$crun_source_dir/crun" /usr/bin/crun
printf 'crun source commit: %s\n' "$crun_source_commit"
/usr/bin/crun --version
sha256sum /usr/bin/crun
git -C "$source_dir" init -q
git -C "$source_dir" remote add origin https://github.com/containers/podman.git
git -C "$source_dir" fetch --depth=1 origin "$source_commit"
git -C "$source_dir" checkout --detach FETCH_HEAD
test "$(git -C "$source_dir" rev-parse HEAD)" = "$source_commit"
# No opaque binaries. Source, vendored dependencies and build flags are pinned;
# native netavark/conmon remain distro dependencies, recorded below.
# crun is independently source-pinned above; dpkg metadata still names the old
# package, so executable version/hash and remote runtime selection are checked.
GOTOOLCHAIN=go1.22.12 make -C "$source_dir" bin/podman \
  BUILDTAGS='containers_image_openpgp exclude_graphdriver_btrfs exclude_graphdriver_devicemapper seccomp systemd'
test "$("$source_dir/bin/podman" --version)" = 'podman version 5.0.0'
# Keep the distro executable path so Ubuntu's existing unprivileged-userns
# AppArmor profile applies. This replacement exists ONLY on the disposable job.
sudo install -o root -g root -m 0755 "$source_dir/bin/podman" /usr/bin/podman
printf 'Podman source commit: %s\n' "$source_commit"
/usr/bin/podman --version
/usr/bin/crun --version
conmon --version
dpkg-query -W podman netavark aardvark-dns crun conmon
sha256sum /usr/bin/podman
