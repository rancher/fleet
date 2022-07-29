# Quick Start

Who needs documentation, lets just run this thing!

## Install

Get helm if you don't have it.  Helm 3 is just a CLI and won't do bad insecure
things to your cluster.

```
brew install helm
```

Install the Fleet Helm charts (there's two because we separate out CRDs for ultimate flexibility.)

```shell
helm -n cattle-fleet-system install --create-namespace --wait \
    fleet-crd https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-crd-{{fleet.version}}.tgz
helm -n cattle-fleet-system install --create-namespace --wait \
    fleet https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-{{fleet.version}}.tgz
```

## Add a Git Repo to watch

Change `spec.repo` to your git repo of choice.  Kubernetes manifest files that should
be deployed should be in `/manifests` in your repo.

```bash
cat > example.yaml << "EOF"
apiVersion: fleet.cattle.io/v1alpha1
kind: GitRepo
metadata:
  name: sample
  # This namespace is special and auto-wired to deploy to the local cluster
  namespace: fleet-local
spec:
  # Everything from this repo will be ran in this cluster. You trust me right?
  repo: "https://github.com/rancher/fleet-examples"
  paths:
  - simple
EOF

kubectl apply -f example.yaml
```

## Get Status

Get status of what fleet is doing

```shell
kubectl -n fleet-local get fleet
```

You should see something like this get created in your cluster.

```
kubectl get deploy frontend
```
```
NAME       READY   UP-TO-DATE   AVAILABLE   AGE
frontend   3/3     3            3           116m
```

Enjoy and read the [docs](https://rancher.github.io/fleet).
