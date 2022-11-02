#!/bin/bash

mkdir -p tmp

kubectl config use-context k3d-upstream
kubectl get -A pod,secret,service,ingress -o json > tmp/cluster.json
kubectl get -A gitrepos,clusters,clustergroups,bundles,bundledeployments -o json > tmp/fleet.json
kubectl get -A events > tmp/events.log
helm list -A > tmp/helm.log
kubectl logs -n cattle-fleet-system -l app=fleet-controller > tmp/fleetcontroller.log
kubectl logs -n cattle-fleet-system -l app=fleet-agent > tmp/fleetagent.log

docker logs k3d-upstream-server-0 &> tmp/k3s.log
docker exec k3d-upstream-server-0 sh -c 'cd /var/log/containers; grep -r "." .' > tmp/containers.log

if k3d cluster list -o json | jq '.[].name' | grep -q "downstream"; then
  kubectl config use-context k3d-downstream
  kubectl get -A pod,secret,service,ingress -o json > tmp/cluster-second.json
  kubectl get -A events > tmp/events-downstream.log
  helm list -A > tmp/helm-downstream.log
  kubectl logs -n cattle-fleet-system -l app=fleet-agent > tmp/fleetagent-second.log

  docker logs k3d-downstream-server-0 &> tmp/k3s-downstream.log
  docker exec k3d-downstream-server-0 sh -c 'cd /var/log/containers; grep -r "." .' > tmp/containers-downstream.log
fi
