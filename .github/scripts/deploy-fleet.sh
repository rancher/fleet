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

helm -n cattle-fleet-system upgrade --install --create-namespace --wait fleet-crd charts/fleet-crd
helm upgrade --install fleet charts/fleet \
  -n cattle-fleet-system --create-namespace --wait \
  --set image.repository="$fleetRepo" \
  --set image.tag="$fleetTag" \
  --set agentImage.repository="$agentRepo" \
  --set agentImage.tag="$agentTag" \
  --set agentImage.imagePullPolicy=IfNotPresent

# wait
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
{ grep -q -m 1 "fleet-agent"; kill $!; } < <(kubectl get deployment -n cattle-fleet-system -w)
kubectl -n cattle-fleet-system rollout status deploy/fleet-agent
