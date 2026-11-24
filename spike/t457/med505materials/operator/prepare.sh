#!/bin/bash
# Assemble the MED-505 bundle, seed the neutral source, write config and stage a preparation root.
# Usage: prepare.sh <id> <repository-cache-dir>
set -euo pipefail
ID=$1; RC=$2; S=/srv/med505; P=$S/prep-$ID; B=$P/bundle; BS=$S/bootstrap
rm -rf "$P"; mkdir -p "$B/tools/bin" "$B/tools/metadata/typed-bazel" "$B/tools/cache" "$B/source/lib"
install -m 0555 $S/out/native-preparation.test $B/phebs-typed-worker
install -m 0555 $S/out/native-preparation.test $B/tools/bin/phebs-t451b-native-driver
install -m 0555 $S/public/bazel-9.0.0-linux-x86_64 $B/tools/bin/bazel
install -m 0555 $S/out/gopackagesdriver $B/tools/bin/gopackagesdriver
install -m 0555 $S/out/scip-go $B/tools/bin/scip-go
install -m 0555 $S/out/t451b-native-probe $B/tools/bin/t451b-native-probe
install -m 0444 $S/sysroot/cc-sysroot.zip $B/tools/cc-sysroot.zip
cp -a $S/stdbuild/go $B/tools/go
install -m 0444 $BS/sdk-amd64.BUILD.bazel $B/tools/go/BUILD.bazel
install -m 0444 $BS/sdk-amd64.version.bzl $B/tools/go/version.bzl
install -m 0444 $BS/sdk-amd64.ROOT $B/tools/go/ROOT
install -m 0444 $BS/sdk-amd64.REPO.bazel $B/tools/go/REPO.bazel
install -m 0444 $BS/sdk-amd64.phebs_tools.bzl $B/tools/go/phebs_tools.bzl
install -m 0555 $S/out/builder $B/tools/go/pkg/tool/linux_amd64/phebs-rules-go-builder
install -m 0555 $S/out/pack.exe $B/tools/go/pkg/tool/linux_amd64/phebs-pack
install -m 0444 $BS/aspect.bzl $B/tools/metadata/typed-bazel/aspect.bzl
install -m 0444 $BS/probe.go.txt $B/tools/metadata/typed-bazel/probe.go
install -m 0444 $BS/sdk-amd64.LICENSE $B/tools/metadata/typed-bazel/rules-go-sdk-LICENSE
# Only the content-addressable store: Bazel's extracted repo-contents cache keeps upstream
# modes (some group/world-writable) and is rebuilt from the archives anyway.
mkdir -p $B/tools/cache/repository && cp -a "$RC/content_addressable" $B/tools/cache/repository/
printf 'block bcr.bazel.build\n' > $B/tools/cache/downloader.cfg
cp $S/ws/MODULE.bazel $S/ws/MODULE.bazel.lock $S/ws/go.mod $B/source/; cp $S/ws/lib/BUILD.bazel $S/ws/lib/lib.go $B/source/lib/
M=$(readlink -f /usr/sbin/mkfs.ext4)
chmod -R go-w $B
[ -z "$(find $B -type f -links +1 | head -1)" ] || { echo "bundle has hard links"; exit 1; }
MKFS=sha256:$(sha256sum "$M" | cut -d' ' -f1)  # the post-run chain adds typed-host-tools.json itself
IMG=$(docker image inspect med505-compiler:try1 --format '{{.Id}}')
$S/out/bundlegen -bundle $B -image "$IMG" -out $P
# Neutral source commit: a real git commit of exactly the source files, fixed author/date.
G=$P/neutral-git; mkdir -p $G; cp -a $B/source/. $G/
( cd $G && git init -q -b main && git add -A && GIT_AUTHOR_NAME=phebs GIT_AUTHOR_EMAIL=neutral@example.invalid \
  GIT_COMMITTER_NAME=phebs GIT_COMMITTER_EMAIL=neutral@example.invalid GIT_AUTHOR_DATE=2026-11-24T00:00:00Z \
  GIT_COMMITTER_DATE=2026-11-24T00:00:00Z git commit -q -m 'neutral MED-505 source' && git rev-parse HEAD ) > $P/source-commit
rm -rf $G
cd $S/t454 && PHEBS_TYPED_NATIVE_TRANSPORT=direct python3 native_acceptance.py observe --output $P/deployment.json
PHEBS_PREPARATION_STORE=ws://127.0.0.1:47811 PHEBS_PREPARATION_STORE_PASS="$(cat $S/secret/surreal-pass)" \
  $S/out/native-preparation.test -test.run '^TestNativePreparationSeed$' -typed-preparation-role seed \
  -typed-preparation-commit "$(cat $P/source-commit)" -typed-preparation-seed-out $P/seed.json >/dev/null
python3 - "$P" "$ID" "$IMG" "$S" "$MKFS" <<'PY'
import json,hashlib,sys
P,ID,IMG,S,MKFS=sys.argv[1:]
def H(p): return "sha256:"+hashlib.sha256(open(p,"rb").read()).hexdigest()
cfg={"schema":"phebs-typed-native-preparation-v1","id":ID,"source_commit":open(P+"/source-commit").read().strip(),
 "source":json.load(open(P+"/seed.json"))["source"],"module":"example.invalid/phebs-native-neutral",
 "remote":"https://example.invalid/phebs-native-neutral","root":"//lib:lib","test_sha256":H(S+"/out/native-preparation.test"),
 "helper_sha256":H(S+"/out/phebs"),"engine_sha256":H(S+"/out/surreal"),"inventory_sha256":H(P+"/inventory.json"),
 "profile_sha256":H(P+"/profile.json"),"image_sha256":IMG,"mkfs_sha256":MKFS,
 "deployment_sha256":H(P+"/deployment.json")}
open(P+"/config.json","w").write(json.dumps(cfg,separators=(",",":")))
PY
R=/var/lib/phebs-typed-preparation/$ID; rm -rf $R; install -d -m 0700 /var/lib/phebs-typed-preparation $R
cp $P/config.json $P/inventory.json $P/profile.json $P/deployment.json $R/
install -m 0500 $S/out/native-preparation.test $R/native-preparation.test
cp -a $B $R/bundle
echo "staged $R"
