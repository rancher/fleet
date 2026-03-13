#!/bin/bash
#
# Submit new Fleet version against rancher/rancher.

set -euo pipefail

NEW_FLEET_VERSION="$1"    # e.g. 0.15.1
NEW_CHART_VERSION="$2"    # e.g. 110.0.1

bump_fleet_api() {
    go get -u "github.com/rancher/fleet/pkg/apis@v${NEW_FLEET_VERSION}"
    go mod tidy
}

RANCHER_DIR="${RANCHER_DIR:-"$(dirname -- "$0")/../../../rancher"}"

pushd "${RANCHER_DIR}" > /dev/null

if [ ! -e ~/.gitconfig ]; then
    git config --global user.name "fleet-bot"
    git config --global user.email fleet@suse.de
fi

# Guard: error if rancher/rancher already has this version or a newer one.
if [ ! -f build.yaml ]; then
    printf 'ERROR: build.yaml not found in %s\n' "$(pwd)" >&2
    exit 1
fi

TARGET_VERSION="${NEW_CHART_VERSION}+up${NEW_FLEET_VERSION}"
CURRENT_VERSION=$(grep 'fleetVersion:' build.yaml | awk '{print $2}')

if [ -z "$CURRENT_VERSION" ]; then
    printf 'ERROR: fleetVersion not found in build.yaml\n' >&2
    exit 1
fi

if [ "$CURRENT_VERSION" = "$TARGET_VERSION" ]; then
    printf 'ERROR: rancher/rancher already contains Fleet %s\n' "$TARGET_VERSION" >&2
    exit 1
fi

# Compare only the chart version numbers (before the '+') to detect downgrades.
current_chart="${CURRENT_VERSION%%+*}"
target_chart="${TARGET_VERSION%%+*}"
if [ "$(printf '%s\n%s\n' "$current_chart" "$target_chart" | sort -V | tail -1)" = "$current_chart" ] \
    && [ "$current_chart" != "$target_chart" ]; then
    printf 'ERROR: rancher/rancher already has a newer Fleet version: %s\n' "$CURRENT_VERSION" >&2
    exit 1
fi

# Guard against replacing a final release with a pre-release of the same or older base.
# sort -V treats "0.11.12-rc.3" > "0.11.12" (lexicographic suffix), so pre-release
# vs final requires an explicit check.
current_fleet="${CURRENT_VERSION##*+up}"
if ! printf '%s' "$current_fleet" | grep -q '-' && printf '%s' "$NEW_FLEET_VERSION" | grep -q '-'; then
    target_fleet_base="${NEW_FLEET_VERSION%%-*}"
    if [ "$(printf '%s\n%s\n' "$current_fleet" "$target_fleet_base" | sort -V | tail -1)" = "$current_fleet" ]; then
        printf 'ERROR: rancher/rancher has final Fleet %s; refusing pre-release %s\n' \
            "$current_fleet" "$NEW_FLEET_VERSION" >&2
        exit 1
    fi
fi

sed -i "s/fleetVersion: .*$/fleetVersion: ${TARGET_VERSION}/" build.yaml
go generate
git add build.yaml pkg/buildconfig/constants.go

# Bump the Fleet API when a pkg/apis tag for this exact version exists in the fleet repo.
if git -C ../fleet tag -l "pkg/apis/v${NEW_FLEET_VERSION}" | grep -q .; then
    bump_fleet_api

    pushd pkg/apis > /dev/null
    bump_fleet_api
    popd > /dev/null

    git add go.mod go.sum pkg/apis/go.mod pkg/apis/go.sum
fi

git commit -m "Updating to Fleet v${NEW_FLEET_VERSION}"

popd > /dev/null
