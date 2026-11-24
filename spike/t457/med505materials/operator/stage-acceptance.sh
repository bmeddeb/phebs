#!/bin/bash
# Stage a neutral native acceptance root from a completed preparation chain.
# Usage: stage-acceptance.sh <acceptance-id> <preparation-id>
set -euo pipefail
ID=$1; PID=$2; S=/srv/med505; P=$S/prep-$PID; FIN=$S/final-$PID; O=$S/postrun-out-$PID
R=/var/lib/phebs-typed-acceptance/$ID; B=$S/accbundle-$ID
IMG=$(docker image inspect med505-compiler:try1 --format '{{.Id}}')
MK=$(python3 -c "import json;print(json.load(open('$P/config.json'))['mkfs_sha256'])")
rm -rf $FIN; mkdir -p $FIN
$S/out/bundlegen -final -in $S/postrun-in-$PID -helper $S/out/phebs -mkfs "$MK" -image "$IMG" -out $FIN >/dev/null
rm -rf $B; cp -a $P/bundle $B
install -m 0555 $S/out/phebs $B/phebs-typed-worker
install -m 0555 $S/out/phebs $B/tools/bin/phebs-t451b-native-driver
install -m 0444 $FIN/typed-bazel-selection.json $B/typed-bazel-selection.json
install -m 0444 $FIN/typed-host-tools.json $B/typed-host-tools.json
# Cross-check: the tree's own inventory must equal the sealed final inventory.
X=$(mktemp -d); $S/out/bundlegen -bundle $B -image "$IMG" -out $X >/dev/null 2>&1 || true
cmp -s $X/inventory.json $FIN/inventory.json || { echo "final bundle tree does not match the sealed inventory"; exit 1; }
rm -rf $X
python3 - "$ID" "$P" "$FIN" "$O" "$S" "$IMG" "$MK" > $S/acc-config-$ID.json <<'PY'
import json,hashlib,sys
ID,P,FIN,O,S,IMG,MK=sys.argv[1:]
def H(p): return "sha256:"+hashlib.sha256(open(p,"rb").read()).hexdigest()
pr=json.load(open(O+"/postrun.json")); pc=json.load(open(P+"/config.json"))
cfg={"schema":"phebs-typed-native-acceptance-v1","id":ID,"source_commit":pc["source_commit"],"source":pc["source"],
 "test_sha256":H(S+"/out/native-acceptance.test"),"helper_sha256":H(S+"/out/phebs"),"engine_sha256":H(S+"/out/surreal"),
 "seed_sha256":H(O+"/seed.surql"),"inventory_sha256":H(FIN+"/inventory.json"),"profile_sha256":H(FIN+"/profile.json"),
 "selection_sha256":pr["selection_sha256"],"image_sha256":IMG,"mkfs_sha256":MK,"universe_sha256":pr["universe_sha256"],
 "profile_epoch":pr["profile_epoch"],"deployment_sha256":H(P+"/deployment.json"),
 "policy":{"memory_bytes":4533092352,"scratch_bytes":4573403136,"scratch_inodes":262144,"tasks":294,"descriptors_per_process":128,
  "cpu_quota_micros":200000,"cpu_period_micros":100000,"wall_seconds":300,"output_bytes":16777216,"scip_bytes":2285819}}
assert cfg["inventory_sha256"]==pr["final_inventory_sha256"] and cfg["profile_sha256"]==pr["profile_sha256"] and cfg["seed_sha256"]==pr["seed_sha256"]
sys.stdout.write(json.dumps(cfg,separators=(",",":")))
PY
rm -rf $R; install -d -m 0700 /var/lib/phebs-typed-acceptance $R
install -m 0600 $S/acc-config-$ID.json $R/config.json
install -m 0600 $FIN/inventory.json $R/inventory.json; install -m 0600 $FIN/profile.json $R/profile.json
install -m 0600 $O/seed.surql $R/seed.surql; install -m 0600 $P/deployment.json $R/deployment.json
install -m 0500 $S/out/native-acceptance.test $R/native-acceptance.test; install -m 0500 $S/out/surreal $R/surreal
cp -a $B $R/bundle
echo "staged $R"
