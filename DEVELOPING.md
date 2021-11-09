# Developing

This document contains tips, workflows, and more for developing within this repository.

## Design for Fleet Managing Fleet: Hosted Rancher, Harvester, Rancher Managing Rancher, etc.

Starting with Fleet v0.3.7 and Rancher v2.6.1, scenarios where Fleet is managing Fleet (standalone or Rancher managing Rancher) will result in _two_ `fleet-agent` deployments running every _managed_ Fleet cluster.
The agents will be communicating with two different `fleet-controller` deployments.

```
Local Fleet Cluster          Managed Fleet Cluster             Downstream Cluster
===================          ========================          ======================
fleet-controller  <------->  fleet-agent (downstream)
fleet-agent (local)          fleet-controller  <------------>  fleet-agent (downstream)
                             fleet-agent (local)
```

## Local Development Workflow: Standlone Fleet and Fleet in Rancher

All steps in this guide assume your current working directory is the root of the repository.
Moreover, this guide was written for Unix-like developer environments, so you may need to modify some steps if you are using a non-Unix-like developer environment (i.e. Windows).

### Step 1: Image Preparation

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

### Step 2: Local Cluster Access

We need a local cluster to work with.
For this guide, we will use [k3d](https://github.com/rancher/k3d).

```sh
k3d cluster create <NAME>
```

### Step 3: Generate as Needed

If you have changed Go code, you may need to generate.

```sh
go generate
```

### Step 4: Running the Controller

This step will differ depending on whether you would like to test standalone Fleet or Fleet in Rancher.

#### Option 1/2: Standalone Fleet

Let's build and push our `fleet-agent` (`linux-amd64` image by default), install our Fleet charts, and then replace the controller deployment with our local controller build.

```sh
(
    go fmt ./...
    REPO=$AGENT_REPO make agent-dev
    docker push $AGENT_REPO/fleet-agent:dev
    for i in fleet-system fleet-default fleet-local; do kubectl create namespace $i; done
    helm install -n fleet-system fleet-crd ./charts/fleet-crd
    helm install -n fleet-system fleet --set agentImage.repository=$AGENT_REPO/fleet-agent --set agentImage.imagePullPolicy=Always ./charts/fleet
    kubectl delete deployment -n fleet-system fleet-controller
    go run cmd/fleetcontroller/main.go
)
```

Alternatively, if the agent's code has been unchanged, you can use the latest agent instead.
We'll use the latest Git tag for this, and _assume_ it is available on DockerHub.

```sh
(
    go fmt ./...
    for i in fleet-system fleet-default fleet-local; do kubectl create namespace $i; done
    helm install -n fleet-system fleet-crd ./charts/fleet-crd
    helm install -n fleet-system fleet --set agentImage.tag=$(git tag --sort=taggerdate | tail -1) ./charts/fleet
    kubectl delete deployment -n fleet-system fleet-controller
    go run cmd/fleetcontroller/main.go
)
```

#### Option 2/2: Fleet in Rancher

First, we need to run Rancher locally.
You can use the [Rancher Wiki](https://github.com/rancher/rancher/wiki/Setting-Up-Rancher-2.0-Development-Environment) for information on how to do so.

> If you are unsure about which method you would like use for tunneling to localhost, we recommend [ngrok](https://ngrok.com) or [tunnelware](https://github.com/StrongMonkey/tunnelware).

Now, let's build and push our `fleet-agent` (`linux-amd64` image by default), if applicable.

```sh
(
    go fmt ./...
    REPO=$AGENT_REPO make agent-dev
    docker push $AGENT_REPO/fleet-agent:dev
)
```

In the Rancher Dashboard, navigate to the `fleet-controller` ConfigMap.
This is likely located in a `cattle-fleet-*` or `fleet-*` namespace.
Replace the existing agent-related fields with the following information:

- our agent image and tag
- the image pull policy to `Always` (for iterative development)

Once the ConfigMap has been updated, edit the `fleet-controller` Deployment and scale down its `replicas` to `0`.
With that change, we can now run the controller locally.

```sh
(
    go fmt ./...
    go run cmd/fleetcontroller/main.go
)
```

**Optional:** you can test Rancher's `FleetWorkspaces` feature by moving Fleet clusters to another workspace in the "Continuous Delivery" section of the Rancher UI.
You can create your own workspace using the API or the UI.
Ensure that clusters are in an "Active" state after migration.

### Step 5: Create GitRepo CR(s)

The controller should be running in your terminal window/pane!
You can now create [GitRepo](https://fleet.rancher.io/gitrepo-structure/) custom resource objects and test Fleet locally.

## Updating Fleet Components

1. Update and tag [rancher/build-tekton](https://github.com/rancher/build-tekton)
1. Update the `rancher/tekton-utils` tag in the `GitJob` helm chart in [rancher/gitjob](https://github.com/rancher/gitjob)
1. Update and tag [rancher/gitjob](https://github.com/rancher/gitjob)
1. Copy the `GitJob` helm chart to `./charts/fleet/charts/gitjob` in [rancher/fleet](https://github.com/rancher/fleet)
1. Generate the `GitJob` CRD into a file in [rancher/fleet](https://github.com/rancher/fleet): `go run $GITJOB_REPO/pkg/crdgen/main.go > $FLEET_REPO/charts/fleet-crd/templates/gitjobs-crds.yaml`
1. Update and tag [rancher/fleet](https://github.com/rancher/fleet) (usually as a release candidate) to use those components in a released version of Fleet
