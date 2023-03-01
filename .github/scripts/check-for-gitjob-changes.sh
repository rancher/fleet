#!/bin/sh
set -ue

: "${MAIN_BRANCH:=master}"

if [ "$(git diff --name-only "${MAIN_BRANCH}" -- "charts/fleet/charts/gitjob/" | wc -l)" -gt "0" ]; then
    if ! git diff "${MAIN_BRANCH}" -- "charts/fleet/charts/gitjob/Chart.yaml" | grep -qE 'version: [.0-9]+'; then
        echo "Gitjob needs to be updated in the 'rancher/gitjob' repo first and"
        echo "then the new Gitjob release can be added to Fleet."
        echo
        echo "Manual changes to the following files in this pr are not allowed:"
        git --no-pager diff --name-only "${MAIN_BRANCH}" -- "charts/fleet/charts/gitjob/"
        exit 1
    fi
fi