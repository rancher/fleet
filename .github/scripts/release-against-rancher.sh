#!/bin/bash
#
# Submit new Fleet version against rancher/rancher

set -ue

NEW_FLEET_VERSION="$1"    # e.g. 0.6.0-rc.3
NEW_CHART_VERSION="$2"    # e.g. 101.1.0
BUMP_API="$3"             # bump api if `true`

bump_fleet_api() {
    COMMIT=$1

    go get -u "github.com/rancher/fleet/pkg/apis@${COMMIT}"
    go mod tidy
}

if [ -z "${GITHUB_WORKSPACE:-}" ]; then
    CHARTS_DIR="$(dirname -- "$0")/../../../rancher"
else
    CHARTS_DIR="${GITHUB_WORKSPACE}/rancher"
fi

pushd "${CHARTS_DIR}" > /dev/null

if [ ! -e ~/.gitconfig ]; then
    git config --global user.name "fleet-bot"
    git config --global user.email fleet@suse.de
fi

sed -i -e "s/ENV CATTLE_FLEET_MIN_VERSION=.*$/ENV CATTLE_FLEET_MIN_VERSION=${NEW_CHART_VERSION}+up${NEW_FLEET_VERSION}/g" package/Dockerfile

git add package/Dockerfile

if [ "${BUMP_API}" == "true" ]; then
    pushd ../fleet > /dev/null
        COMMIT=$(git rev-list -n 1 "v${NEW_FLEET_VERSION}")
    popd > /dev/null

    bump_fleet_api "${COMMIT}"

    pushd pkg/apis > /dev/null
        bump_fleet_api "${COMMIT}"
    popd > /dev/null

    git add go.* pkg/apis/go.*
fi

git commit -m "Updating to Fleet v${NEW_FLEET_VERSION}"

popd > /dev/null