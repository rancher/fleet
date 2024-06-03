#!/bin/bash

set -euxo pipefail

external_ip="${external_ip-172.18.0.1.sslip.io}"
cluster="${cluster-k3d-upstream}"

kubectl config use-context "$cluster"
kubectl apply -f https://github.com/jetstack/cert-manager/releases/download/v1.5.4/cert-manager.yaml
kubectl wait --for=condition=Available deployment --timeout=2m -n cert-manager --all

helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
# set CATTLE_SERVER_URL and CATTLE_BOOTSTRAP_PASSWORD to get rancher out of "bootstrap" mode
helm upgrade rancher rancher-latest/rancher \
  --install --wait \
  --create-namespace \
  --namespace cattle-system \
  --set hostname="$external_ip" \
  --set bootstrapPassword=admin \
  --set "extraEnv[0].name=CATTLE_SERVER_URL" \
  --set "extraEnv[0].value=https://$external_ip" \
  --set "extraEnv[1].name=CATTLE_BOOTSTRAP_PASSWORD" \
  --set "extraEnv[1].value=rancherpassword"

# wait for deployment of rancher
kubectl -n cattle-system rollout status deploy/rancher

# wait for rancher to create fleet namespace, deployment and controller
{ grep -q -m 1 "fleet"; kill $!; } < <(kubectl get deployments -n cattle-fleet-system -w)
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller

helm list -A
