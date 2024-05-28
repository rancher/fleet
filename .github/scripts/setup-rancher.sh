#!/bin/bash

set -euxo pipefail

public_hostname="${public_hostname-172.18.0.1.sslip.io}"
fleetns="${fleetns-cattle-fleet-system}"
upstream_ctx="${FLEET_E2E_CLUSTER-k3d-upstream}"
version="${1-v2.7.0}"

helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
helm repo update rancher-latest

kubectl config use-context "$upstream_ctx"

kubectl apply -f https://github.com/jetstack/cert-manager/releases/download/v1.5.4/cert-manager.yaml
kubectl wait --for=condition=Available deployment --timeout=2m -n cert-manager --all

# set CATTLE_SERVER_URL and CATTLE_BOOTSTRAP_PASSWORD to get rancher out of "bootstrap" mode
helm upgrade rancher rancher-latest/rancher --version "$version" \
  --devel \
  --install --wait \
  --create-namespace \
  --namespace cattle-system \
  --set replicas=1 \
  --set hostname="$public_hostname" \
  --set bootstrapPassword=admin \
  --set "extraEnv[0].name=CATTLE_SERVER_URL" \
  --set "extraEnv[0].value=https://$public_hostname" \
  --set "extraEnv[1].name=CATTLE_BOOTSTRAP_PASSWORD" \
  --set "extraEnv[1].value=rancherpassword"

# wait for deployment of rancher
kubectl -n cattle-system rollout status deploy/rancher

# wait for rancher to create fleet namespace, deployment and controller
{ grep -q -m 1 "fleet"; kill $!; } < <(kubectl get deployments -n "$fleetns" -w)
kubectl -n "$fleetns" rollout status deploy/fleet-controller
{ grep -E -q -m 1 "fleet-agent-local.*1/1"; kill $!; } < <(kubectl get bundles -n fleet-local -w)
