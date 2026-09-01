#!/usr/bin/env bash
# Drives skillsctl for the README demo recording.
#
#   go build -o skillsctl ./cmd/skillsctl
#   asciinema rec --window-size 112x46 -c "$(pwd)/assets/demo_script.sh" assets/demo.cast --overwrite
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

[[target]]
name = "gemini"
dir = "$DEMO/agents/gemini"

[[target]]
name = "cursor"
dir = "$DEMO/agents/cursor"

[[target]]
name = "windsurf"
dir = "$DEMO/agents/windsurf"
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

prompt "skillsctl install mattpocock/skills"
expect <<EXPECT
set timeout 15
log_user 0
spawn $SKILLSCTL install mattpocock/skills
log_user 1
expect "agents to install into:"
after 800
send "j"
after 500
send "j"
after 500
send " "
after 800
send "\r"
expect "enter install"
after 800
send " "
after 800
send "\r"
expect eof
EXPECT
echo
sleep 2

run "skillsctl install mattpocock/skills --skill teach --agent claude,codex" \
  "$SKILLSCTL" install mattpocock/skills --skill teach --agent claude,codex

run "skillsctl list" \
  "$SKILLSCTL" list

run "skillsctl update --dry-run" \
  "$SKILLSCTL" update --dry-run

run "skillsctl remove teach" \
  "$SKILLSCTL" remove teach

sleep 1
