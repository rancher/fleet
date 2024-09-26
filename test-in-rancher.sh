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

until kubectl get service -n kube-system traefik -o jsonpath='{.status.loadBalancer.ingress[0].ip}'; do sleep 3; done
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
    --set crds.enabled=true \
    --wait \
    --wait-for-jobs

helm upgrade --install rancher rancher-latest/rancher \
    --devel \
    --namespace cattle-system \
    --create-namespace \
    --set hostname=$ip.sslip.io \
    --set bootstrapPassword=admin \
    --set "extraEnv[0].name=CATTLE_SERVER_URL" \
    --set "extraEnv[0].value=https://$ip.sslip.io" \
    --set "extraEnv[1].name=CATTLE_AGENT_TLS_MODE" \
    --set "extraEnv[1].value=system-store" \
    --set replicas=1 \
#    --set rancherImageTag=$tag \
#    --set extraEnv[0].name=CATTLE_FLEET_VERSION \
#    --set extraEnv[0].value=999.9.9+up9.9.9 \
#    --set extraEnv[1].name=CATTLE_CHART_DEFAULT_URL \
#    --set extraEnv[1].value=https://github.com/fleetrepoci/charts \
#    --set extraEnv[2].name=CATTLE_CHART_DEFAULT_BRANCH \
#    --set extraEnv[2].value=$branch \

     # only needed for 2.7 which doesn't support CATTLE_FLEET_VERSION (nor version pinning btw, needs additional cherry
     # picks; hopefully we soon won't have to deal with that branch anymore)
    #--set extraEnv[0].name=CATTLE_FLEET_MIN_VERSION \

# wait for deployment of rancher
kubectl -n cattle-system rollout status deploy/rancher

# wait for rancher to create fleet namespace, deployment and controller
until kubectl get deployments -n cattle-fleet-system | grep -q "fleet"; do sleep 3; done
kubectl -n cattle-fleet-system rollout status deploy/fleet-controller

until kubectl get bundles -n fleet-local | grep -q "fleet-agent-local.*1/1"; do sleep 3; done

helm list -A

export public_hostname=$ip.sslip.io

./.github/scripts/wait-for-loadbalancer.sh
./.github/scripts/register-downstream-clusters.sh
