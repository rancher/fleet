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

echo "Creating import cluster 'second'..."
$rancher_cli clusters create second --import

echo "Waiting for cluster ID to be assigned..."
timeout=60
elapsed=0
until $rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .ID' | grep -Eq "c-[a-z0-9]" ; do
  if [ $elapsed -ge $timeout ]; then
    echo "ERROR: Timeout waiting for cluster ID"
    $rancher_cli cluster ls
    exit 1
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done
id=$( $rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .ID' )
echo "Cluster ID: $id"

echo "Getting import command..."
timeout=60
elapsed=0
until $rancher_cli cluster import "$id" | grep -q curl; do
  if [ $elapsed -ge $timeout ]; then
    echo "ERROR: Timeout waiting for import command"
    exit 1
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

echo "Applying import manifest to downstream cluster..."
kubectl config use-context "$cluster_downstream"
import_cmd=$($rancher_cli cluster import "$id" | grep curl)
echo "Running: $import_cmd"
echo "$import_cmd" | sh

echo "Verifying cattle-system resources were created..."
sleep 10
kubectl get pods -n cattle-system
kubectl get deployments -n cattle-system

echo "Checking cattle-cluster-agent deployment..."
if kubectl get deployment -n cattle-system cattle-cluster-agent &>/dev/null; then
  kubectl describe deployment -n cattle-system cattle-cluster-agent
  echo ""
  echo "Cattle cluster agent logs (if available):"
  kubectl logs -n cattle-system -l app=cattle-cluster-agent --tail=50 || echo "No logs available yet"
else
  echo "WARNING: cattle-cluster-agent deployment not found"
fi

echo "Waiting for cluster to become active..."
timeout=600  # 10 minutes
elapsed=0
while true; do
  state=$($rancher_cli cluster ls --format json | jq -r 'select(.Name=="second") | .Cluster.state' || echo "unknown")

  if [ "$state" = "active" ]; then
    echo "Cluster is now active!"
    break
  fi

  if [ $elapsed -ge $timeout ]; then
    echo "ERROR: Timeout waiting for cluster registration after ${timeout}s"
    echo "Current cluster state: $state"
    echo ""
    echo "=== Cluster details from Rancher ==="
    $rancher_cli cluster ls --format json | jq 'select(.Name=="second")'
    echo ""
    echo "=== Checking downstream cluster context ==="
    kubectl config use-context "$cluster_downstream"
    echo "Pods in cattle-system namespace:"
    kubectl get pods -n cattle-system || true
    echo ""
    echo "Checking for cattle-cluster-agent:"
    kubectl get deployments -n cattle-system cattle-cluster-agent -o yaml || true
    echo ""
    echo "cattle-cluster-agent logs:"
    kubectl logs -n cattle-system -l app=cattle-cluster-agent --tail=100 || true
    echo ""
    echo "=== Checking network connectivity ==="
    echo "Testing connection to Rancher from downstream cluster:"
    kubectl run test-connection --image=curlimages/curl:latest --rm -i --restart=Never -- \
      curl -k -v "https://${public_hostname}/healthz" || true
    exit 1
  fi

  echo "Waiting for cluster registration (current state: $state, elapsed: ${elapsed}s)"
  sleep 5
  elapsed=$((elapsed + 5))
done

kubectl config use-context "$ctx"

# Wait for Fleet agent to be ready on downstream cluster
echo "Waiting for Fleet agent bundle to be ready..."
timeout=300  # 5 minutes
elapsed=0
until kubectl get bundles -n fleet-default | grep -q "fleet-agent-$id.*1/1"; do
  if [ $elapsed -ge $timeout ]; then
    echo "ERROR: Timeout waiting for Fleet agent bundle to be ready"
    echo "Current bundles in fleet-default:"
    kubectl get bundles -n fleet-default
    echo ""
    echo "BundleDeployments:"
    kubectl get bundledeployments -A
    echo ""
    echo "Fleet agent pods on downstream:"
    kubectl config use-context "$cluster_downstream"
    kubectl get pods -n cattle-fleet-system || true
    kubectl logs -n cattle-fleet-system -l app=fleet-agent --tail=100 || true
    exit 1
  fi
  sleep 3
  elapsed=$((elapsed + 3))
done
echo "Fleet agent bundle is ready!"
