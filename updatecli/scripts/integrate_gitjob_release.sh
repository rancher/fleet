#!/bin/sh

set -e

# Testing that we can run curl command from the GitHub Runner
curl --help > /dev/null

VERSION="$1"
if [ -z "$VERSION" ]
then
    echo "Empty version provided"
    exit 0
fi

CURRENT_VERSION="$(sed -n "s/^version: \([.0-9]*\)/\1/p" ./charts/fleet/charts/gitjob/Chart.yaml)"

if [ "$VERSION" = "$CURRENT_VERSION" ]
then
    echo "The Gitjob chart in Fleet is already up to date."
    exit 0
fi

if test "$DRY_RUN" == "true"
then
    echo "**DRY_RUN** Gitjob would be bumped from ${CURRENT_VERSION} to ${VERSION}"
    exit 0
fi

curl -sS -L -o "/tmp/gitjob-${VERSION}.tgz" "https://github.com/rancher/gitjob/releases/download/v${VERSION}/gitjob-${VERSION}.tgz"

tar -xf "/tmp/gitjob-${VERSION}.tgz" -C ./charts/fleet/charts/

# move gitjob crd to fleet-crd chart
mv ./charts/fleet/charts/gitjob/templates/crds.yaml ./charts/fleet-crd/templates/gitjobs-crds.yaml

# update gitjob version reference in Fleet chart
gsed -z -i "s/  name: gitjob\n  version: [0-9a-z.-]*/  name: gitjob\n  version: ${VERSION}/" ./charts/fleet/Chart.yaml

rm "/tmp/gitjob-${VERSION}.tgz"
