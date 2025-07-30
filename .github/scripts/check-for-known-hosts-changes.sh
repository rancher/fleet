#!/bin/sh
set -u

if [ $# -eq 0 ]; then
    echo "Usage: $0 <base_branch>"
    exit 1
fi

base=$1

git fetch origin $base
git diff --quiet origin/$base HEAD -- charts/fleet/templates/configmap_known_hosts.yaml
if [ $? -eq 1 ]; then # The PR contains changes to the config map
    .github/scripts/update_known_hosts_configmap.sh

    git diff --quiet charts/fleet/templates/configmap_known_hosts.yaml
    if [ $? -eq 1 ]; then
        echo "Locally-computed changes for the known-hosts config map do not match initial changes."
        exit 1
    fi
fi
