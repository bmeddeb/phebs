#!/bin/bash
# Run the post-run chain for a completed preparation id.
set -euo pipefail
ID=$1; S=/srv/med505; R=/var/lib/phebs-typed-preparation/$ID; P=$S/prep-$ID; I=$S/postrun-in-$ID; O=$S/postrun-out-$ID
rm -rf $I $O; mkdir -p $I; cp $R/config.json $R/result.json $R/receipt.json $R/inventory.json $I/; cp $P/seed.json $I/provenance.json
PATH=$S/out:$PATH PATH=$S/out:$PATH PHEBS_PREPARATION_STORE=ws://127.0.0.1:47811 PHEBS_PREPARATION_STORE_PASS="$(cat $S/secret/surreal-pass)" \
  $S/out/native-preparation.test -test.run '^TestNativePreparationPostRun$' -test.v -typed-preparation-role postrun \
  -typed-preparation-postrun-in $I -typed-preparation-postrun-out $O -typed-preparation-profile-epoch 1 \
  -typed-preparation-cmd-helper $S/out/phebs 2>&1 | grep -v '^=== RUN' | tail -6 | cut -c1-500
ls -la $O
