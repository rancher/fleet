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

CURRENT_VERSION="$(sed -n 's/.*.Values.tekton.tag | default "v\([.0-9]*\).*"/\1/p' ./chart/templates/deployment.yaml)"

if [ "$VERSION" -eq "$CURRENT_VERSION" ]
then
    echo "The Tekton chart in Gitjob is already up to date."
    exit 0
fi


if test "$DRY_RUN" == "true"
then
    echo "**DRY_RUN** Tekton would be bumped from ${CURRENT_VERSION} to ${VERSION}"
    exit 0
fi

sed -e -i "s/.Values.tekton.tag | default \"v[.0-9]*\"/.Values.tekton.tag | default \"v${VERSION}\"/g" ./chart/templates/deployment.yaml