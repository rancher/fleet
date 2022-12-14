#!/bin/bash

set -euxo pipefail

assets=$(curl -s https://api.github.com/repos/rancher/fleet/releases | jq -r "sort_by(.tag_name) | [ .[] | select(.draft | not) ] | .[-1].assets")
crd_url=$(echo "$assets" | jq -r '.[] | select(.name | test("fleet-crd-.*.tgz")) | .browser_download_url')
controller_url=$(echo "$assets" | jq -r '.[] | select(.name | test("fleet-\\d.*.tgz")) | .browser_download_url')
helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet-crd "$crd_url"
helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet "$controller_url"

# wait for controller and agent rollout
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
{ grep -E -q -m 1 "fleet-agent-local.*1/1"; kill $!; } < <(kubectl get bundles -n fleet-local -w)
kubectl -n cattle-fleet-system rollout status deploy/fleet-agent
