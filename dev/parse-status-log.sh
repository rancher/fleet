#!/usr/bin/env bash
# parse-status-log.sh - Summarize status change diffs from bundle-monitor logs
#
# Usage:
#   cat logs.json | ./dev/parse-status-log.sh
#   kubectl logs <pod> | ./dev/parse-status-log.sh
#   ./dev/parse-status-log.sh < logs.json

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
RESET='\033[0m'

# Disable colors if not a terminal
if [[ ! -t 1 ]]; then
    RED='' GREEN='' CYAN='' YELLOW='' BOLD='' RESET=''
fi

while IFS= read -r line; do
    # Skip lines that aren't status-change events
    event=$(echo "$line" | jq -r '.event // empty' 2>/dev/null) || continue
    [[ "$event" == "status-change" ]] || continue

    ts=$(echo "$line" | jq -r '.ts // "?"')
    bundle=$(echo "$line" | jq -r '.bundle // "?"')
    gitrepo=$(echo "$line" | jq -r '.gitrepo // "?"')
    diff=$(echo "$line" | jq -r '.diff // empty')

    [[ -z "$diff" ]] && continue

    echo -e "${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo -e "${BOLD}Time:${RESET}    $ts"
    echo -e "${BOLD}Bundle:${RESET}  $bundle"
    echo -e "${BOLD}GitRepo:${RESET} $gitrepo"
    echo -e "${BOLD}Changes:${RESET}"

    # Extract the meaningful diff lines (- and + prefixed) with context
    echo "$diff" | while IFS= read -r dline; do
        if [[ "$dline" =~ ^-[[:space:]] ]]; then
            echo -e "  ${RED}$dline${RESET}"
        elif [[ "$dline" =~ ^\+[[:space:]] ]]; then
            echo -e "  ${GREEN}$dline${RESET}"
        fi
    done

    echo ""
done
