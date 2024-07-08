#!/bin/bash

set -euxo

branch=${CHARTS_BRANCH-""}
tag=${RANCHER_TAG-"test-fleet"}

# Build Rancher
# This is not needed if all you want is to test Fleet against an existing Rancher release
pushd ../rancher
TARGET_REPO=rancher/rancher:$tag ./dev-scripts/build-local.sh
popd

# XXX: this could be improved by using a single server instead of 3; calling this script is lazy but overkill
./dev/setup-k3d

k3d image import rancher/rancher:$tag -m direct -c upstream

ip=$(kubectl get service -n kube-system traefik -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

if [ -z $branch ]; then
    echo "TODO run test charts release workflow:"
    echo "https://github.com/rancher/fleet/actions/workflows/release-against-test-charts.yml"
    exit 1
fi

helm repo update
helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --set installCRDs=true \
    --set extraArgs[0]=--enable-certificate-owner-ref=true

helm upgrade --install rancher rancher-latest/rancher \
    --devel \
    --namespace cattle-system \
    --create-namespace \
    --set hostname=$ip.sslip.io \
    --set bootstrapPassword=admin \
    --set replicas=1 \
    --set rancherImageTag=$tag \
    --set extraEnv[0].name=CATTLE_FLEET_VERSION \
    --set extraEnv[0].value=999.9.9+up9.9.9 \
    --set extraEnv[1].name=CATTLE_CHART_DEFAULT_URL \
    --set extraEnv[1].value=https://github.com/fleetrepoci/charts \
    --set extraEnv[2].name=CATTLE_CHART_DEFAULT_BRANCH \
    --set extraEnv[2].value=$branch \
    --set extraEnv[3].name=CATTLE_AGENT_TLS_MODE \
    --set extraEnv[3].value=strict
     # only needed for 2.7 which doesn't support CATTLE_FLEET_VERSION (nor version pinning btw, needs additional cherry
     # picks; hopefully we soon won't have to deal with that branch anymore)
    #--set extraEnv[0].name=CATTLE_FLEET_MIN_VERSION \
