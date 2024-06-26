#!/bin/bash

set -euxo pipefail

# usage: ./deploy-fleet.sh ghcr.io/rancher/fleet:sha-49f6f81 ghcr.io/rancher/fleet-agent:1h
if [ $# -ge 2 ] && [ -n "$1" ] && [ -n "$2" ]; then
  fleetRepo="${1%:*}"
  fleetTag="${1#*:}"
  agentRepo="${2%:*}"
  agentTag="${2#*:}"
else
  fleetRepo="rancher/fleet"
  fleetTag="dev"
  agentRepo="rancher/fleet-agent"
  agentTag="dev"
fi

host=$(kubectl get node k3d-upstream-server-0 -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
ca=$( kubectl config view --flatten -o jsonpath='{.clusters[?(@.name == "k3d-upstream")].cluster.certificate-authority-data}' | base64 -d )
server="https://$host:6443"

helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet-crd charts/fleet-crd
helm upgrade --install fleet charts/fleet \
  -n cattle-fleet-system --create-namespace --wait \
  --set image.repository="$fleetRepo" \
  --set image.tag="$fleetTag" \
  --set agentImage.repository="$agentRepo" \
  --set agentImage.tag="$agentTag" \
  --set agentImage.imagePullPolicy=IfNotPresent \
  --set apiServerCA="$ca" \
  --set apiServerURL="$server" \

# wait for controller and agent rollout
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
{ grep -E -q -m 1 "fleet-agent-local.*1/1"; kill $!; } < <(kubectl get bundles -n fleet-local -w)
kubectl -n cattle-fleet-system rollout status deploy/fleet-agent

# label local cluster
kubectl patch clusters.fleet.cattle.io -n fleet-local local --type=json -p '[{"op": "add", "path": "/metadata/labels/management.cattle.io~1cluster-display-name", "value": "local" }]'
