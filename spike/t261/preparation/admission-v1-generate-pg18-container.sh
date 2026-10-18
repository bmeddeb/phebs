#!/bin/bash
# Frozen supplementary public-DDL capture plan. Lead executes only after slot
# allocation and exact digest/version admission. Never run application corpora.
set -euo pipefail

pg_bin=/usr/lib/postgresql/18/bin
pg_pid=
cleanup() {
    status=$?
    trap - EXIT
    if [[ -n "$pg_pid" ]]; then
        if ! "$pg_bin/pg_ctl" --pgdata=/work/data --mode=fast --wait --timeout=10 stop >&2; then
            kill -KILL "$pg_pid" 2>/dev/null || true
        fi
        server_status=0
        wait "$pg_pid" || server_status=$?
        printf 't261_server_exit=%s\n' "$server_status" >&2
        printf 't261_server_joined=true\n' >&2
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

test "$(id -u)" = 999
test ! -e /work/data
test "$(sha256sum /fixture/input.sql | cut -d ' ' -f 1)" = dd7153ba4733be4d24b26993f8762d98d05d8754691194c41266f824c2253273
for binary in postgres initdb psql pg_dump pg_ctl pg_isready; do
    version=$("$pg_bin/$binary" --version)
    printf '%s\n' "$version" >&2
    [[ "$version" =~ PostgreSQL\)\ 18\.6([\ \(]|$) ]]
    sha256sum "$pg_bin/$binary" >&2
done
sha256sum /bin/bash /bin/sh /usr/bin/id /usr/bin/cut /usr/bin/mkdir /usr/bin/sleep /usr/bin/wc /usr/bin/cmp /usr/bin/sha256sum >&2

mkdir -m 0700 /work/socket /work/tmp
export TMPDIR=/work/tmp
"$pg_bin/initdb" --pgdata=/work/data --username=t261 --encoding=UTF8 --locale=C --auth-local=trust --auth-host=reject --no-instructions >&2
"$pg_bin/postgres" -D /work/data -c listen_addresses= -c unix_socket_directories=/work/socket -c unix_socket_permissions=0700 -c port=52611 -c max_connections=4 -c shared_buffers=16MB -c max_wal_size=64MB -c min_wal_size=32MB -c logging_collector=off > /work/postgres.log 2>&1 &
pg_pid=$!
ready=false
for ((attempt=0; attempt<100; attempt++)); do
    if "$pg_bin/pg_isready" --host=/work/socket --port=52611 --username=t261 --dbname=postgres > /dev/null; then
        ready=true
        break
    fi
    kill -0 "$pg_pid"
    sleep 0.05
done
test "$ready" = true
"$pg_bin/psql" -X --no-password --set=ON_ERROR_STOP=1 --host=/work/socket --port=52611 --username=t261 --dbname=postgres --file=/fixture/input.sql >&2
for pass in 1 2; do
    "$pg_bin/pg_dump" --schema-only --no-owner --no-privileges --no-comments --no-security-labels --no-tablespaces --restrict-key=phebsT261PinnedPG186Fixture --no-password --host=/work/socket --port=52611 --username=t261 --dbname=postgres > "/work/dump${pass}.sql"
    test "$(wc -c < "/work/dump${pass}.sql")" -le 65536
    sha256sum "/work/dump${pass}.sql" >&2
done
cmp -s /work/dump1.sql /work/dump2.sql
printf 't261_double_dump_identical=true\n' >&2
cat /work/dump1.sql
