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

# avoid a downgrade by rancher
sed -i 's/^version: 0/version: 9000/' charts/fleet-crd/Chart.yaml
helm upgrade fleet-crd charts/fleet-crd  --wait -n cattle-fleet-system

until helm -n cattle-fleet-system status fleet | grep -q "STATUS: deployed"; do echo waiting for original fleet chart to be deployed; sleep 3; done

# avoid a downgrade by rancher
sed -i 's/^version: 0/version: 9000/' charts/fleet/Chart.yaml

helm upgrade fleet charts/fleet \
  --reset-then-reuse-values \
  --wait -n cattle-fleet-system \
  --create-namespace \
  --set image.repository="$fleetRepo" \
  --set image.tag="$fleetTag" \
  --set agentImage.repository="$agentRepo" \
  --set agentImage.tag="$agentTag" \
  --set agentImage.imagePullPolicy=IfNotPresent

kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
helm list -A

# wait for fleet agent bundle for downstream cluster
sleep 5
{ grep -E -q -m 1 "fleet-agent-c.*1/1"; kill $!; } < <(kubectl get bundles -n fleet-default -w)

kubectl config use-context k3d-downstream
kubectl -n cattle-fleet-system rollout status deployment/fleet-agent

helm list -A
