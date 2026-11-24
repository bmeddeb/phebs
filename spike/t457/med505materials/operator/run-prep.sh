#!/bin/bash
# Run the native preparation host step for a staged id and print the outcome.
ID=$1; R=/var/lib/phebs-typed-preparation/$ID; L=/var/lib/phebs-typed-preparation/$ID.host.log
cd $R && s=$(date +%s); timeout 900 ./native-preparation.test -test.run '^TestNativePreparationHost$' -test.v \
  -typed-preparation-role host -typed-preparation-config $R/config.json > $L 2>&1; rc=$?
echo "rc=$rc secs=$(( $(date +%s)-s ))"; grep -v '^=== RUN' $L | tail -8 | cut -c1-600
if [ -f $R/failed-command.json ]; then python3 -c "
import json,base64;d=json.load(open('$R/failed-command.json'));print('stage',d.get('command'),'exit',d.get('exit'));print(base64.b64decode(d.get('stderr_prefix','')).decode(errors='replace')[-1500:])"; fi
ls $R
