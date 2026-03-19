#!/bin/bash
#
# Compute the Fleet and Rancher versions needed to open a release PR.
#
# Environment variables:
#   FLEET_BRANCH     Fleet branch to release (e.g. release/v0.15 or main)
#   GH_TOKEN         GitHub token for API calls
#   FLEET_REPO_DIR   Path to the fleet checkout (default: ./fleet)
#   GITHUB_OUTPUT    Path to the GitHub Actions output file

set -euo pipefail

FLEET_REPO_DIR="${FLEET_REPO_DIR:-./fleet}"

# Derive the Fleet minor version from the branch name.
# For main, compute it as (highest release branch minor) + 1.
# NOTE: Update 'v0.*' pattern if Fleet has a major version bump (e.g., search for 'v1.*').
if [ "$FLEET_BRANCH" = "main" ]; then
    highest_minor=$(git -C "$FLEET_REPO_DIR" ls-remote --heads origin 'refs/heads/release/v0.*' \
        | grep -oE 'v0\.[0-9]+' | cut -d. -f2 | sort -n | tail -1)
    if [ -z "$highest_minor" ]; then
        printf 'ERROR: No release/v0.* branches found in fleet repo\n' >&2
        exit 1
    fi
    fleet_minor=$((highest_minor + 1))
else
    fleet_minor=$(printf '%s' "$FLEET_BRANCH" | grep -oE '[0-9]+$')
fi

# Calculate Rancher minor version based on Fleet minor.
# NOTE: Update this formula if the version relationship changes (currently Fleet minor - 1).
rancher_minor=$((fleet_minor - 1))
# NOTE: Update '2' to '3' (or next major version) if Rancher has a major version bump.
charts_branch="dev-v2.${rancher_minor}"

# Fetch the Fleet chart directory listing from the rancher/charts dev branch.
chart_response=$(curl -fsSL \
    -H "Authorization: Bearer ${GH_TOKEN}" \
    "https://api.github.com/repos/rancher/charts/contents/charts/fleet?ref=${charts_branch}") || {
    printf 'ERROR: Could not list Fleet charts in rancher/charts branch %s\n' "$charts_branch" >&2
    exit 1
}

# Prefer a final (non-pre-release) chart over an RC: sort -V ranks "0.14.4-rc.5"
# above "0.14.4" because the RC has extra characters, so without filtering the
# RC would be selected even after the final chart has been published.
all_charts=$(printf '%s' "$chart_response" \
    | jq -r '.[] | select(.type == "dir") | .name')
latest_chart=$(printf '%s' "$all_charts" | { grep -v '+up.*-' || true; } | sort -V | tail -1)
if [ -z "$latest_chart" ]; then
    latest_chart=$(printf '%s' "$all_charts" | sort -V | tail -1)
fi

if [ -z "$latest_chart" ]; then
    printf 'ERROR: No Fleet chart directories found in rancher/charts branch %s\n' "$charts_branch" >&2
    exit 1
fi

# Chart directory names follow the pattern <chart-version>+up<fleet-version>,
# e.g. 110.0.1+up0.15.1.
new_fleet="${latest_chart##*+up}"
new_chart="${latest_chart%%+*}"

# Target the Rancher release branch when it exists; fall back to main.
# NOTE: Update '2' to '3' (or next major version) if Rancher has a major version bump.
rancher_ref="release/v2.${rancher_minor}"
http_status=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${GH_TOKEN}" \
    "https://api.github.com/repos/rancher/rancher/branches/release%2Fv2.${rancher_minor}")
case "$http_status" in
    200) ;;
    404) rancher_ref="main" ;;
    *) printf 'ERROR: GitHub API returned HTTP %s while checking Rancher branch\n' "$http_status" >&2; exit 1 ;;
esac

printf 'Charts branch:     %s\n' "$charts_branch"
printf 'New Fleet version: %s\n' "$new_fleet"
printf 'New chart version: %s\n' "$new_chart"
printf 'Rancher ref:       %s\n' "$rancher_ref"

{
    printf 'new_fleet=%s\n' "$new_fleet"
    printf 'new_chart=%s\n' "$new_chart"
    printf 'rancher_ref=%s\n' "$rancher_ref"
} >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT is not set}"
