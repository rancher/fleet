#!/bin/bash

set -euxo pipefail

shards_json=${SHARDS-""}
node=${NODE-k3d-upstream-server-0}

function eventually {
  for _ in $(seq 1 3); do
    "$@" && return 0
  done
  return 1
}

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

host=$(kubectl get node $node -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
ca=$( kubectl config view --flatten -o jsonpath='{.clusters[?(@.name == "k3d-upstream")].cluster.certificate-authority-data}' | base64 -d )
server="https://$host:6443"

# Constructing the shards settings dynamically
shards_settings=""
if [ -n "$shards_json" ]; then
  index=0
  for shard in $(echo "${shards_json}" | jq -c '.[]'); do
    shard_id=$(echo "$shard" | jq -r '.id')
    shards_settings="$shards_settings --set shards[$index].id=$shard_id"
    node_selector=$(echo "$shard" | jq -r '.nodeSelector // empty')
    if [ -n "$node_selector" ]; then
      for key in $(echo "$node_selector" | jq -r 'keys[]'); do
        value=$(echo "$node_selector" | jq -r --arg key "$key" '.[$key]')
        escaped_key=$(echo "$key" | sed 's/\./\\./g')
        shards_settings="$shards_settings --set shards[$index].nodeSelector.$escaped_key=$value"
      done
    fi
    index=$((index + 1))
  done
fi

eventually helm upgrade --install fleet-crd charts/fleet-crd \
  --atomic \
  -n cattle-fleet-system \
  --create-namespace
eventually helm upgrade --install fleet charts/fleet \
  --atomic \
  -n cattle-fleet-system \
  --create-namespace \
  --set image.repository="$fleetRepo" \
  --set image.tag="$fleetTag" \
  --set agentImage.repository="$agentRepo" \
  --set agentImage.tag="$agentTag" \
  --set agentImage.imagePullPolicy=IfNotPresent \
  --set apiServerCA="$ca" \
  --set apiServerURL="$server" \
  $shards_settings \
  --set-string extraEnv[0].name=EXPERIMENTAL_OCI_STORAGE \
  --set-string extraEnv[0].value=true \
  --set-string extraEnv[1].name=EXPERIMENTAL_HELM_OPS \
  --set-string extraEnv[1].value=true \
  --set garbageCollectionInterval=1s \
  --set debug=true --set debugLevel=1

# wait for controller and agent rollout
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller
{ grep -E -q -m 1 "fleet-agent-local.*1/1"; kill $!; } < <(kubectl get bundles -n fleet-local -w)
kubectl -n cattle-fleet-system rollout status deployment/fleet-agent

# label local cluster
kubectl patch clusters.fleet.cattle.io -n fleet-local local --type=json -p '[{"op": "add", "path": "/metadata/labels/management.cattle.io~1cluster-display-name", "value": "local" }]'
