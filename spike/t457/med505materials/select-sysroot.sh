#!/bin/bash
# Run inside the MED-505 compiler image with /out mounted. Writes:
#   packages.txt  installed packages in the no-recommends closure of gcc-13 + libc6-dev
#   files.txt     regular files those packages own (documentation excluded), plus
#                 lib/ and lib64/ aliases of the merged-usr shared objects that glibc's
#                 linker scripts and the compiler wrapper name by their /lib paths
#   files.tar     those files, symlinks dereferenced, for sysrootzip
set -euo pipefail
export LC_ALL=C
deps=$(apt-cache depends --recurse --no-recommends --no-suggests --no-conflicts \
  --no-breaks --no-replaces --no-enhances gcc-13 libc6-dev | grep -v '^ ' | sed 's/:amd64$//' | sort -u)
installed=$(dpkg-query -W -f='${Package}\n' | sort -u)
comm -12 <(echo "$deps") <(echo "$installed") > /out/packages.txt
while read -r p; do dpkg -L "$p"; done < /out/packages.txt |
  grep -vE '^/usr/share/(doc|man|info|locale|lintian|bug|gdb)(/|$)' | sort -u |
  while read -r f; do [ -f "$f" ] && echo "${f#/}"; done > /tmp/owned.txt
{
  cat /tmp/owned.txt
  grep -E '^usr/lib/x86_64-linux-gnu/[^/]+\.so(\.[0-9.]+)?$' /tmp/owned.txt | sed 's#^usr/##'
  grep -E '^usr/lib64/[^/]+$' /tmp/owned.txt | sed 's#^usr/##'
} | sort -u > /out/files.txt
cd /
tar --dereference --hard-dereference --no-recursion --numeric-owner --owner=0 --group=0 -cf /out/files.tar -T /out/files.txt
wc -l < /out/packages.txt
wc -l < /out/files.txt
