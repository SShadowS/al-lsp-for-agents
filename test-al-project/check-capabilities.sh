#!/bin/bash
# Find the newest wrapper log and compare capabilities against baseline.
#
# Usage:
#   ./check-capabilities.sh                  # compare against baseline
#   ./check-capabilities.sh --update-baseline # update the baseline snapshot
#   ./check-capabilities.sh --show-only       # just show current capabilities

cd "$(dirname "$0")"

# Newest log from a Claude Code session. The test harnesses write wrapper logs
# too, and their client capabilities are not Claude Code's.
LOG=""
for f in $(ls -t "$TEMP"/al-lsp-wrapper-go-*.log 2>/dev/null); do
  if grep -q -m1 '"clientInfo":{"name":"Claude Code"' "$f"; then
    LOG="$f"
    break
  fi
done
if [ -z "$LOG" ]; then
  echo "No wrapper log from Claude Code found in \$TEMP ($TEMP)."
  echo "Use Claude Code with the AL plugin first to generate a log."
  exit 1
fi

echo "Using log: $LOG"
python test_client_capabilities.py --from-log "$LOG" "$@"
