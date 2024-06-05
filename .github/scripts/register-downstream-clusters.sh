#!/bin/bash

set -euxo pipefail


public_hostname="${public_hostname-172.18.0.1.sslip.io}"
cluster_downstream="${cluster_downstream-k3d-downstream}"
ctx=$(kubectl config current-context)

# hardcoded token, cluster is ephemeral and private
token="token-ci:zfllcbdr4677rkj4hmlr8rsmljg87l7874882928khlfs2pmmcq7l5"

user=$(kubectl get users -o go-template='{{range .items }}{{.metadata.name}}{{"\n"}}{{end}}' | tail -1)
sed "s/user-zvnsr/$user/" <<'EOF' | kubectl apply -f -
apiVersion: management.cattle.io/v3
kind: Token
authProvider: local
current: false
description: mytoken
expired: false
expiresAt: ""
isDerived: true
lastUpdateTime: ""
metadata:
  generateName: token-
  labels:
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

echo -e "4\n" | rancher login "https://$public_hostname" --token "$token" --skip-verify

rancher clusters create second --import
until rancher cluster ls --format json | jq -r 'select(.Name=="second") | .ID' | grep -Eq "c-[a-z0-9]" ; do sleep 1; done
id=$( rancher cluster ls --format json | jq -r 'select(.Name=="second") | .ID' )

kubectl config use-context "$cluster_downstream"
rancher cluster import "$id"
rancher cluster import "$id" | grep curl | sh

until rancher cluster ls --format json | jq -r 'select(.Name=="second") | .Cluster.state' | grep -q active; do
  echo waiting for cluster registration
  sleep 5
done

kubectl config use-context "$ctx"
