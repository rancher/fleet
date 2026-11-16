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

# List the entries of a rancher/charts directory; fails on a truncated listing.
list_entries() {
    curl -fsSL \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        "https://api.github.com/repos/rancher/charts/git/trees/$1:$2" \
        | jq -r --arg type "$3" 'if .truncated then error("truncated") else
              .tree[] | select(.type == $type) | .path | sub("\\.yaml$"; "") end'
}

# Fleet publishes its charts to the OCI catalog, which rancher/charts mirrors into
# `entries/fleet` on `staging`, one file per chart version.
charts_ref="staging"
catalog_charts=$(list_entries "$charts_ref" entries/fleet blob) || {
    printf 'ERROR: Could not list Fleet charts in the rancher/charts OCI catalog\n' >&2
    exit 1
}
all_charts=$(printf '%s\n' "$catalog_charts" | grep -E "\+up[0-9]+\.${fleet_minor}\." || true)

# Charts built from `main` are only published to the catalog, so they never use the
# `charts/fleet` directory of a `dev-v2.X` branch, which predates the catalog.
if [ -z "$all_charts" ] && [ "$FLEET_BRANCH" != "main" ]; then
    charts_ref="dev-v2.${rancher_minor}"
    all_charts=$(list_entries "$charts_ref" charts/fleet tree) || {
        printf 'ERROR: Could not list Fleet charts in rancher/charts branch %s\n' "$charts_ref" >&2
        exit 1
    }
fi

# Find the latest chart by chart-version prefix (part before '+up'), then
# prefer a final fleet release over an RC with the same chart-version prefix.
# sort -V ranks "0.14.4-rc.5" above "0.14.4" because the RC suffix is longer,
# so we must compare explicitly rather than relying on sort order alone.
# This still picks an RC when it is genuinely the newest chart (e.g. 0.15.0-rc.6
# when no final 0.15.0 chart exists yet), rather than falling back to an older
# final chart from a previous release cycle.
highest_chart_prefix=$(printf '%s' "$all_charts" | sed 's/+up.*//' | sort -V | tail -1)
latest_chart=$(printf '%s' "$all_charts" | grep "^${highest_chart_prefix}+up" | grep -v '+up.*-' | sort -V | tail -1 || true)
if [ -z "$latest_chart" ]; then
    latest_chart=$(printf '%s' "$all_charts" | grep "^${highest_chart_prefix}+up" | sort -V | tail -1 || true)
fi

if [ -z "$latest_chart" ]; then
    printf 'ERROR: No Fleet 0.%s chart found in rancher/charts (%s)\n' "$fleet_minor" "$charts_ref" >&2
    exit 1
fi

# Chart names follow the pattern <chart-version>+up<fleet-version>, e.g.
# 110.0.1+up0.15.1, whether an entry file name or a chart directory name.
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

printf 'New Fleet version: %s\n' "$new_fleet"
printf 'New chart version: %s\n' "$new_chart"
printf 'Rancher ref:       %s\n' "$rancher_ref"

{
    printf 'new_fleet=%s\n' "$new_fleet"
    printf 'new_chart=%s\n' "$new_chart"
    printf 'rancher_ref=%s\n' "$rancher_ref"
} >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT is not set}"
