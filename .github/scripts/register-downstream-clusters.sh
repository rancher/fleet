#!/bin/bash

set -euxo pipefail


public_hostname="${public_hostname-172.28.0.2.sslip.io}"
cluster_downstream="${cluster_downstream-k3d-downstream}"
ctx=$(kubectl config current-context)
if command -v rancher-cli &> /dev/null; then
  rancher_cli="rancher-cli"
elif command -v rancher &> /dev/null; then
  rancher_cli="rancher"
else
  echo "Neither rancher-cli nor rancher found in PATH"
  exit 1
fi

# Get Rancher token
rancherpassword=$(kubectl get secret --namespace cattle-system bootstrap-secret -o go-template='{{.data.bootstrapPassword|base64decode}}')
login_response=$(curl -k -s -X POST "https://${public_hostname}/v3-public/localProviders/local?action=login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"${rancherpassword}\"}")
token=$(echo "$login_response" | jq -r '.token')
if [ "$token" = "null" ] || [ -z "$token" ]; then
  echo "Failed to get authentication token"
  exit 1
fi

# Log into the 4th project listed by `rancher login`, which should be the local cluster's default project.
echo -e "4\n" | $rancher_cli login "https://$public_hostname" --token "$token" --skip-verify

$rancher_cli clusters create second --import
until $rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .ID' | grep -Eq "c-[a-z0-9]" ; do sleep 1; done
id=$( $rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .ID' )

until $rancher_cli cluster import "$id" | grep -q curl; do sleep 1; done
kubectl config use-context "$cluster_downstream"
$rancher_cli cluster import "$id" | grep curl | sh

until $rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .Cluster.state' | grep -q active; do
  echo waiting for cluster registration
  sleep 5
done

kubectl config use-context "$ctx"

# Wait for Fleet agent to be ready on downstream cluster
until kubectl get bundles -n fleet-default | grep -q "fleet-agent-$id.*1/1"; do sleep 3; done
