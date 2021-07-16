k3d cluster delete
k3d cluster create
VERSION=0.3.5
helm -n fleet-system install --create-namespace --wait \
    fleet-crd https://github.com/rancher/fleet/releases/download/v${VERSION}/fleet-crd-${VERSION}.tgz
helm -n fleet-system install --create-namespace --wait \
    fleet https://github.com/rancher/fleet/releases/download/v${VERSION}/fleet-${VERSION}.tgz
sleep 50
helm uninstall -n fleet-system fleet
kubectl create ns cattle-fleet-system
helm install -n cattle-fleet-system fleet --set agentImage.tag=v0.3.6-rc4 --set agentImage.imagePullPolicy=Always ./charts/fleet
kubectl delete deployment -n cattle-fleet-system fleet-controller
NAMESPACE=cattle-fleet-system go run cmd/fleetcontroller/main.go
