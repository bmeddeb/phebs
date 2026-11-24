#!/bin/bash
# Run one native acceptance case for a staged id.
ID=$1; CASE=$2; R=/var/lib/phebs-typed-acceptance/$ID; L=/var/lib/phebs-typed-acceptance/$ID.$CASE.log
cd $R && s=$(date +%s.%N); timeout 700 ./native-acceptance.test -test.run '^TestTypedNativeAcceptance$' -test.v \
  -typed-native-config $R/config.json -typed-native-case $CASE > $L 2>&1; rc=$?
echo "case=$CASE rc=$rc secs=$(python3 -c "print(round($(date +%s.%N)-$s,1))")"; grep -v '^=== RUN' $L | tail -10 | cut -c1-700
