# Developing

This document contains tips, workflows, and more for developing within this repository.

## Local Development

All steps in this guide assume your current working directory is the root of the repository.
Moreover, this guide was written for Unix-like developer environments, so you may need to modify some steps if you are using a non-Unix-like developer environment (i.e. Windows).

### Preparation

We need to use a registry to store `fleet-agent` developer builds.
Using a personal [DockerHub](https://hub.docker.com/) repository is usually a suitable choice.
The full repository name must be `<your-choice>/fleet-agent`.

Now, we need export an environment variable with our repository name as the value.
This will be used when building, pushing, and deploying our agent.

> Note: the value for this variable should not include `/fleet-agent`.
> For example, if your full DockerHub repository name is `foobar/fleet-agent`, the value used below should be `foobar`.

Export the new `AGENT_REPO` variable and use the aforementioned value.

```
export AGENT_REPO=<your-OCI-image-repository>
```

### Live Controller Development

Create a local cluster.
For this guide, we will use [k3d](https://github.com/rancher/k3d).

```sh
k3d cluster create <NAME>
```

Now, we will build and push our `fleet-agent`, install our Fleet charts, and then replace the controller deployment with our local controller build.

```sh
(
    REPO=$AGENT_REPO make agent-dev
    docker push $AGENT_REPO/fleet-agent:dev
    for i in fleet-system fleet-default fleet-local; do kubectl create namespace $i; done
    helm install -n fleet-system fleet-crd ./charts/fleet-crd
    helm install -n fleet-system fleet --set agentImage.repository=$AGENT_REPO/fleet-agent --set agentImage.imagePullPolicy=Always ./charts/fleet
    kubectl delete deployment -n fleet-system fleet-controller
    go run cmd/fleetcontroller/main.go
)
```

The controller should be running in your terminal window/pane!
You can now create [GitRepo](https://fleet.rancher.io/gitrepo-structure/) custom resource objects and test Fleet locally.
