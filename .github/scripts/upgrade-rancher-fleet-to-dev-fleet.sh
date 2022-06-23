#!/bin/bash

set -euxo pipefail

# only supports sha tags, e.g.: ghcr.io/rancher/fleet:sha-49f6f81
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

kubectl config use-context k3d-upstream

until helm -n cattle-fleet-system status fleet-crd  | grep -q "STATUS: deployed"; do echo waiting for original fleet-crd chart to be deployed; sleep 1; done

helm upgrade fleet-crd charts/fleet-crd  --wait -n cattle-fleet-system

until helm -n cattle-fleet-system status fleet | grep -q "STATUS: deployed"; do echo waiting for original fleet chart to be deployed; sleep 3; done

helm upgrade fleet charts/fleet \
  --wait -n cattle-fleet-system \
  --create-namespace \
  --set image.repository="$fleetRepo" \
  --set image.tag="$fleetTag" \
  --set agentImage.repository="$agentRepo" \
  --set agentImage.tag="$agentTag" \
  --set agentImage.imagePullPolicy=IfNotPresent

kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
helm list -A

kubectl config use-context k3d-downstream

# wait for fleet to be up on downstream cluster
sleep 30
{ grep -q -m 1 "cattle-fleet-system"; kill $!; } < <(kubectl get namespace -w)
{ grep -q -m 1 "fleet-agent"; kill $!; } < <(kubectl get deployment -n cattle-fleet-system -w)
# FLAKE: rollout fails: object has been deleted
# https://github.com/manno/fleet/issues/4
kubectl -n cattle-fleet-system rollout status deploy/fleet-agent
# FLAKE: infinite loop
until helm list -A | grep -q "fleet-agent.*cattle-fleet-system.*deployed"; do
  echo waiting for fleet agent helm chart
  helm list -A
  sleep 3
done
helm list -A
