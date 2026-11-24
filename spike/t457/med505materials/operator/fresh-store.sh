#!/bin/bash
# Restart the isolated in-memory SurrealDB used for the pristine neutral seed.
S=/srv/med505
pid=$(pgrep -f '^/srv/med505/out/surreal start --bind 127.0.0.1:47811') && kill $pid && sleep 2
nohup $S/out/surreal start --bind 127.0.0.1:47811 --user root --pass "$(cat $S/secret/surreal-pass)" memory > $S/secret/surreal.log 2>&1 &
sleep 3; curl -s http://127.0.0.1:47811/health -o /dev/null -w "store health=%{http_code}\n"
