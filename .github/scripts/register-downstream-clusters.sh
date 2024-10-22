#!/bin/bash

set -euxo pipefail


public_hostname="${public_hostname-172.28.0.2.sslip.io}"
cluster_downstream="${cluster_downstream-k3d-downstream}"
ctx=$(kubectl config current-context)

# hardcoded token, cluster is ephemeral and private
token="token-ci:zfllcbdr4677rkj4hmlr8rsmljg87l7874882928khlfs2pmmcq7l5"

user=$(kubectl get users -o go-template='{{range .items }}{{.metadata.name}}{{"\n"}}{{end}}' | tail -1)
sed "s/user-zvnsr/$user/" <<'EOF' | kubectl apply -f -
apiVersion: management.cattle.io/v3
kind: Token
authProvider: local
metadata:
  labels:
    authn.management.cattle.io/kind: kubeconfig
    authn.management.cattle.io/token-userId: user-zvnsr
    cattle.io/creator: norman
  name: token-ci
ttl: 0
token: zfllcbdr4677rkj4hmlr8rsmljg87l7874882928khlfs2pmmcq7l5
userId: user-zvnsr
userPrincipal:
  displayName: Default Admin
  loginName: admin
  me: true
  metadata:
    creationTimestamp: null
    name: local://user-zvnsr
  principalType: user
  provider: local
EOF

# Log into the 4th project listed by `rancher login`, which should be the local cluster's default project.
echo -e "4\n" | rancher login "https://$public_hostname" --token "$token" --skip-verify

rancher clusters create second --import
until rancher cluster ls --format json | jq -r 'select(.Name=="second") | .ID' | grep -Eq "c-[a-z0-9]" ; do sleep 1; done
id=$( rancher cluster ls --format json | jq -r 'select(.Name=="second") | .ID' )

kubectl config use-context "$cluster_downstream"
kubectl create clusterrolebinding cluster-admin-binding --clusterrole cluster-admin --user $user

until [ -n "$(rancher cluster import "$id" | grep curl)" ]; do sleep 1; done
rancher cluster import "$id" | grep curl | sh

until rancher cluster ls --format json | jq -r 'select(.Name=="second") | .Cluster.state' | grep -q active; do
  echo waiting for cluster registration
  sleep 5
done

kubectl config use-context "$ctx"

# Wait for Fleet agent to be ready on downstream cluster
until kubectl get bundles -n fleet-default | grep -q "fleet-agent-$id.*1/1"; do sleep 3; done
