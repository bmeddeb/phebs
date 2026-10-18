#!/bin/sh
# Build a signable image without changing the selected checkout or its Git data.
set -eu

if [ "$#" -ne 2 ]; then
  echo 'usage: build-release-binary.sh OUTPUT VERSION (from a clean repository root)' >&2
  exit 2
fi
output=$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")
version=$2
case "$version" in
  ''|*[!0-9A-Za-z.+-]*) echo 'invalid build version' >&2; exit 2 ;;
esac
root=$(git rev-parse --show-toplevel)
root=$(cd "$root" && pwd -P)
if [ "$root" != "$(pwd -P)" ]; then
  echo 'release binary must be built from the repository root' >&2
  exit 2
fi
commit=$(git rev-parse --verify HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo 'release binary requires a clean checkout; preserve local edits and use a clean worktree' >&2
  exit 2
fi
scratch=$(mktemp -d "${TMPDIR:-/tmp}/phebs-release-build.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
trap 'exit 1' HUP INT TERM
source=$root
if [ -f "$root/.git" ]; then
  # The pinned Go toolchain does not detect worktree .git files. A local
  # shared clone preserves the actual commit and gives Go a real .git directory.
  git clone --quiet --shared --no-checkout -- "$root" "$scratch/source"
  git -C "$scratch/source" checkout --quiet --detach "$commit"
  mkdir -p "$scratch/source/ui"
  cp -R "$root/ui/dist" "$scratch/source/ui/dist"
  source=$scratch/source
fi
(
  cd "$source"
  GOWORK=off go build -buildvcs=true -trimpath -tags ui \
    -ldflags "-X main.version=$version" -o "$scratch/phebs" ./cmd/phebs
)
go version -m "$scratch/phebs" | awk -v commit="$commit" '
  $1 == "build" && $2 == "vcs=git" { git = 1 }
  $1 == "build" && $2 == "vcs.revision=" commit { revision = 1 }
  $1 == "build" && $2 == "vcs.modified=false" { clean = 1 }
  END { exit !(git && revision && clean) }
' || { echo 'release binary lacks exact clean Git build metadata' >&2; exit 1; }
if [ "$(git rev-parse HEAD)" != "$commit" ] || [ -n "$(git status --porcelain)" ]; then
  echo 'source checkout changed during the release build' >&2
  exit 1
fi
if [ -d "$output" ]; then
  echo 'release binary output must be a file' >&2
  exit 2
fi
mv -f "$scratch/phebs" "$output"
