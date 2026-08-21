#!/usr/bin/env bash
# Drives skillsctl for the README demo recording.
#
#   go build -o skillsctl ./cmd/skillsctl
#   asciinema rec -c "$(pwd)/assets/demo_script.sh" assets/demo.cast --overwrite
#   agg assets/demo.cast assets/demo.gif
#
# Installs from a real clone of https://github.com/mattpocock/skills into a
# throwaway store and agent dirs under /tmp/skillsctl-demo, so the recording
# never touches the operator's real ~/.claude or ~/.codex.
set -euo pipefail

SKILLSCTL="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/skillsctl"
DEMO=/tmp/skillsctl-demo
rm -rf "$DEMO"
mkdir -p "$DEMO/agents/claude" "$DEMO/agents/codex"

export SKILLSCTL_HOME="$DEMO/store"
export SKILLSCTL_CONFIG="$DEMO/config.toml"
cat >"$SKILLSCTL_CONFIG" <<EOF
[[target]]
name = "claude"
dir = "$DEMO/agents/claude"

[[target]]
name = "codex"
dir = "$DEMO/agents/codex"
EOF

prompt() {
  printf '\033[1;32m$\033[0m %s\n' "$1"
  sleep 1
}

run() {
  prompt "$1"
  shift
  "$@" || true
  echo
  sleep 2
}

run "skillsctl install mattpocock/skills" \
  "$SKILLSCTL" install mattpocock/skills </dev/null
sleep 1

run "skillsctl install mattpocock/skills --skill teach" \
  "$SKILLSCTL" install mattpocock/skills --skill teach

run "skillsctl list" \
  "$SKILLSCTL" list

run "skillsctl update --dry-run" \
  "$SKILLSCTL" update --dry-run

run "skillsctl remove teach" \
  "$SKILLSCTL" remove teach

sleep 1
