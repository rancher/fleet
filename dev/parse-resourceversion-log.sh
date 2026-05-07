#!/usr/bin/env bash
# parse-resourceversion-log.sh - Summarize resource version change diffs from bundle-monitor logs
#
# Usage:
#   cat logs.json | ./dev/parse-resourceversion-log.sh
#   kubectl logs <pod> | ./dev/parse-resourceversion-log.sh
#   ./dev/parse-resourceversion-log.sh < logs.json

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

# Disable colors if not a terminal
if [[ ! -t 1 ]]; then
    RED='' GREEN='' CYAN='' YELLOW='' MAGENTA='' BOLD='' DIM='' RESET=''
fi

while IFS= read -r line; do
    # Skip lines that aren't resourceversion-change events
    event=$(echo "$line" | jq -r '.event // empty' 2>/dev/null) || continue
    [[ "$event" == "resourceversion-change" ]] || continue

    ts=$(echo "$line" | jq -r '.ts // "?"')
    bundle=$(echo "$line" | jq -r '.bundle // "?"')
    gitrepo=$(echo "$line" | jq -r '.gitrepo // "?"')
    commit=$(echo "$line" | jq -r '.commit // "?"')
    old_rv=$(echo "$line" | jq -r '.oldResourceVersion // "?"')
    new_rv=$(echo "$line" | jq -r '.newResourceVersion // "?"')
    reason=$(echo "$line" | jq -r '.reason // "?"')
    metadata_changes=$(echo "$line" | jq -r '(.metadataChanges // []) | join(", ")')
    diff=$(echo "$line" | jq -r '.diff // empty')

    echo -e "${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo -e "${BOLD}Time:${RESET}     $ts"
    echo -e "${BOLD}Bundle:${RESET}   $bundle"
    echo -e "${BOLD}GitRepo:${RESET}  $gitrepo"
    echo -e "${BOLD}Commit:${RESET}   ${DIM}${commit:0:12}${RESET}"
    echo -e "${BOLD}Version:${RESET}  ${YELLOW}$old_rv${RESET} → ${GREEN}$new_rv${RESET}"
    echo -e "${BOLD}Reason:${RESET}   $reason"
    [[ -n "$metadata_changes" ]] && echo -e "${BOLD}Changed:${RESET}  ${MAGENTA}$metadata_changes${RESET}"

    if [[ -n "$diff" ]]; then
        echo -e "${BOLD}Diff:${RESET}"
        echo "$diff" | while IFS= read -r dline; do
            if [[ "$dline" =~ ^changed: ]]; then
                echo -e "  ${YELLOW}$dline${RESET}"
            elif [[ "$dline" =~ ^added: ]]; then
                echo -e "  ${GREEN}$dline${RESET}"
            elif [[ "$dline" =~ ^removed: ]]; then
                echo -e "  ${RED}$dline${RESET}"
            elif [[ "$dline" =~ ^-[[:space:]] ]]; then
                echo -e "  ${RED}$dline${RESET}"
            elif [[ "$dline" =~ ^\+[[:space:]] ]]; then
                echo -e "  ${GREEN}$dline${RESET}"
            else
                echo -e "  ${DIM}$dline${RESET}"
            fi
        done
    fi

    echo ""
done
