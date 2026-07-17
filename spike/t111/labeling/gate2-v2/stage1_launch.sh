#!/bin/zsh
# GATE2-V2 Stage-1 launcher. Sources the operator's token file (never stored
# in the plist or repo), then fires the accepted runner exactly once.
set -u
ENV_FILE="$HOME/.config/phebs/stage1.env"
if [ ! -f "$ENV_FILE" ]; then
  echo "stage1_launch: $ENV_FILE missing — create it with: GITHUB_TOKEN=..." >&2
  exit 2
fi
set -a; source "$ENV_FILE"; set +a
exec /usr/bin/python3 /Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage1_snapshot.py --now
