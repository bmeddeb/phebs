#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
	printf 'usage: install-surreal-ci.sh WORK_DIRECTORY\n' >&2
	exit 2
fi

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
version=$(tr -d '[:space:]' < "$root/.surrealdb-version")
expected=$(tr -d '[:space:]' < "$root/.surrealdb-linux-amd64.sha256")
work_dir=$1
archive="$work_dir/surreal-v$version.linux-amd64.tgz"
bin_dir="$work_dir/surreal-bin"

mkdir -p "$bin_dir"
curl --fail --silent --show-error --location \
	"https://github.com/surrealdb/surrealdb/releases/download/v$version/surreal-v$version.linux-amd64.tgz" \
	--output "$archive"
printf '%s  %s\n' "$expected" "$archive" | sha256sum --check -
tar -xzf "$archive" -C "$bin_dir"
chmod 0755 "$bin_dir/surreal"

if [ "$("$bin_dir/surreal" version | awk '{print $1}')" != "$version" ]; then
	printf 'downloaded SurrealDB did not report version %s\n' "$version" >&2
	exit 1
fi
printf '%s\n' "$bin_dir" >> "${GITHUB_PATH:?GITHUB_PATH is required}"
