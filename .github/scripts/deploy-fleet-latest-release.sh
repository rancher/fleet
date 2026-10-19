#!/bin/bash

set -euxo pipefail

assets=$(curl -s https://api.github.com/repos/rancher/fleet/releases | jq -r "sort_by(.tag_name) | [ .[] | select(.draft | not) ] | .[-1].assets")
crd_url=$(echo "$assets" | jq -r '.[] | select(.name | test("fleet-crd-.*.tgz")) | .browser_download_url')
controller_url=$(echo "$assets" | jq -r '.[] | select(.name | test("fleet-\\d.*.tgz")) | .browser_download_url')
helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet-crd "$crd_url"
helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet "$controller_url"

# wait for controller and agent rollout
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
# Bounded wait: a plain `kubectl get bundles -w` never terminates on its own,
# which used to hang this step until the job timed out. Polling also tolerates
# the brief window where the local cluster import deletes and recreates
# fleet-agent-local, which makes `kubectl wait --for=jsonpath` fail on NotFound.
timeout 300 bash -c 'until kubectl -n fleet-local get bundle/fleet-agent-local \
  -o jsonpath="{.status.display.readyClusters}" 2>/dev/null | grep -q "1/1"; do sleep 2; done'
kubectl -n cattle-fleet-system rollout status deploy/fleet-agent
