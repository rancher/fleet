#!/bin/bash
# format-monitor-summary.sh - Parse and display Fleet Monitor summary in a readable format
#
# Usage:
#   cat logfile.log | ./format-monitor-summary.sh
#   ./format-monitor-summary.sh < logfile.log
#   ./format-monitor-summary.sh logfile.log

set -euo pipefail

# Check if jq is installed
if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Please install jq first." >&2
    exit 1
fi

# Read input (either from file argument, pipe, or stdin)
if [ $# -gt 0 ]; then
    input=$(cat "$1")
else
    input=$(cat)
fi

# Find all lines with "Fleet Monitor Summary" and write to a temp file
tmp_summaries=$(mktemp)
trap 'rm -f "$tmp_summaries"' EXIT
echo "$input" | grep '"msg":"Fleet Monitor Summary"' > "$tmp_summaries" || true

if [ ! -s "$tmp_summaries" ]; then
    echo "Error: No 'Fleet Monitor Summary' log line found in input" >&2
    exit 1
fi

# Extract first and last summary lines (using file to avoid SIGPIPE with pipefail)
first_json=$(head -1 "$tmp_summaries" | grep -o '{"level":"info".*}')
json=$(tail -1 "$tmp_summaries" | grep -o '{"level":"info".*}')

# Calculate time range across all summaries
first_ts=$(echo "$first_json" | jq -r '.summary.timestamp')
last_ts=$(echo "$json" | jq -r '.summary.timestamp')
summary_count=$(wc -l < "$tmp_summaries" | tr -d ' ')

# Extract summary data
summary=$(echo "$json" | jq -r '.msg')
timestamp=$(echo "$json" | jq -r '.summary.timestamp')
interval=$(echo "$json" | jq -r '.summary.interval_seconds')
total_resources=$(echo "$json" | jq -r '.summary.totals.total_resources_monitored')
total_events=$(echo "$json" | jq -r '.summary.totals.total_events')

# Print header
echo "================================================================================"
echo "  FLEET MONITOR SUMMARY"
echo "================================================================================"
echo "  Timestamp:        $timestamp"
echo "  Interval:         ${interval}s"
echo "  Total Resources:  $total_resources"
echo "  Total Events:     $total_events"
echo "================================================================================"
echo

# Function to print a resource type table
print_resource_table() {
    local resource_type=$1
    local data=$(echo "$json" | jq -r ".summary.summary.\"$resource_type\"")

    if [ "$data" = "null" ] || [ -z "$data" ]; then
        return
    fi

    echo "▼ $resource_type"
    echo "-------------------------------------------------------------------------------"

    # Get all resource names
    local resources=$(echo "$data" | jq -r 'keys[]')

    if [ -z "$resources" ]; then
        echo "  No resources"
        echo
        return
    fi

    # Calculate maximum resource name length
    local max_len=8  # Minimum width for "RESOURCE" header
    while IFS= read -r resource; do
        local len=${#resource}
        if [ $len -gt $max_len ]; then
            max_len=$len
        fi
    done <<< "$resources"

    # Add some padding
    max_len=$((max_len + 2))

    # Print table header
    printf "  %-${max_len}s %8s %8s %8s %8s %8s %8s %8s %8s %8s\n" "RESOURCE" "CREATE" "DELETE" "N-FOUND" "STATUS" "GEN-CHG" "ANNOT" "LABEL" "RESVER" "EVENTS"
    local separator=$(printf '%*s' $max_len | tr ' ' '-')
    printf "  %-${max_len}s %8s %8s %8s %8s %8s %8s %8s %8s %8s\n" "$separator" "------" "------" "-------" "------" "-------" "-----" "-----" "------" "------"

    # Print each resource
    while IFS= read -r resource; do
        local create=$(echo "$data" | jq -r ".\"$resource\".create // 0")
        local deletion=$(echo "$data" | jq -r ".\"$resource\".deletion // 0")
        local not_found=$(echo "$data" | jq -r ".\"$resource\".\"not-found\" // 0")
        local status_change=$(echo "$data" | jq -r ".\"$resource\".\"status-change\" // 0")
        local gen_change=$(echo "$data" | jq -r ".\"$resource\".\"generation-change\" // 0")
        local annot_change=$(echo "$data" | jq -r ".\"$resource\".\"annotation-change\" // 0")
        local label_change=$(echo "$data" | jq -r ".\"$resource\".\"label-change\" // 0")
        local resver_change=$(echo "$data" | jq -r ".\"$resource\".\"resourceversion-change\" // 0")
        local total_events=$(echo "$data" | jq -r ".\"$resource\".total_events // 0")

        printf "  %-${max_len}s %8d %8d %8d %8d %8d %8d %8d %8d %8d\n" \
            "$resource" "$create" "$deletion" "$not_found" "$status_change" "$gen_change" "$annot_change" "$label_change" "$resver_change" "$total_events"

        # Print triggered-by if present
        local triggered_by=$(echo "$data" | jq -r ".\"$resource\".\"triggered-by\" // null")
        if [ "$triggered_by" != "null" ]; then
            echo "$triggered_by" | jq -r 'to_entries[] | "    └─ triggered-by: \(.key) = \(.value)"'
        fi
    done <<< "$resources"

    echo
}

# Print tables for each resource type
resource_types=$(echo "$json" | jq -r '.summary.summary | keys[]')

while IFS= read -r resource_type; do
    print_resource_table "$resource_type"
done <<< "$resource_types"

echo "================================================================================"

# Calculate and display time range
if [ "$first_ts" != "$last_ts" ]; then
    first_epoch=$(date -d "$first_ts" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%S" "${first_ts%%.*}" +%s 2>/dev/null)
    last_epoch=$(date -d "$last_ts" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%S" "${last_ts%%.*}" +%s 2>/dev/null)
    duration_s=$(( last_epoch - first_epoch ))
    hours=$(( duration_s / 3600 ))
    minutes=$(( (duration_s % 3600) / 60 ))
    seconds=$(( duration_s % 60 ))
    echo "  Time range:       $first_ts"
    echo "                 -> $last_ts"
    printf "  Duration:         %02dh %02dm %02ds  (%d summaries)\n" "$hours" "$minutes" "$seconds" "$summary_count"
else
    echo "  Time range:       $first_ts  (single summary)"
fi
echo "================================================================================"
