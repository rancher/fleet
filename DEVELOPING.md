# Developing

This document contains tips, workflows, and more for local development within this repository.

More documentation for maintainers and developers of Fleet can be found in [docs/](docs/).

## Fleet Standalone - For Running E2E Tests

Development scripts are provided under `/dev` to make it easier setting up a local development Fleet standalone environment and running the E2E tests against it. These scripts are intended only for local Fleet development, not for production nor any other real world scenario.

Setting up the local development environment and running the E2E tests is described in the [/dev/README.md](/dev/README.md).

---

## Fleet in Rancher

All steps in this guide assume your current working directory is the root of the repository.
Moreover, this guide was written for Unix-like developer environments, so you may need to modify some steps if you are using a non-Unix-like developer environment (i.e. Windows).

### Step 1: Image Preparation

We need to use a registry to store `fleet-agent` developer builds.
Using a personal [DockerHub](https://hub.docker.com/) repository is usually a suitable choice.
The full repository name must be `<your-choice>/fleet-agent`.

Now, we need export an environment variable with your repository name as the value.
This will be used when building, pushing, and deploying your agent.

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

First, we need to run Rancher locally.
You can use the [Rancher Wiki](https://github.com/rancher/rancher/wiki/Setting-Up-Rancher-2.0-Development-Environment) for information on how to do so.

> If you are unsure about which method you would like use for tunneling to localhost, we recommend [ngrok](https://ngrok.com) or [tunnelware](https://github.com/StrongMonkey/tunnelware).

Now, let's build and push your `fleet-agent` (`linux-amd64` image by default), if applicable.

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

- your agent image and tag
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

---

## Fleet in Rancher - Using a Custom Fork

If you need to test Fleet *during Rancher startup*, you may want to use a custom fork.
The following steps can help you do so:

1. Fork [rancher/fleet](https://github.com/rancher/fleet) and commit your changes to a branch of your choice
2. Change image names and tags for the Helm chart(s) for your custom development image(s) accordingly
3. Publish images corresponding to the names and tags changed in the Helm chart(s)
4. Tag your **_fork_** with a [SemVer-compliant](https://semver.org/) tag that's "greater" than the Fleet chart tag in your chosen version of Rancher _(note: the exact tag name is not that important, but we want it to be "grater" just in case there's a collision for the "latest" version)_
5. Fork [rancher/charts](https://github.com/rancher/charts) and update branch `dev-v2.x` with your changes to the `fleet`, `fleet-crd`, and `fleet-agent` packages
6. You'll need to change the chart URL to your charts' `tgz` location (this may need to be self-hosted)
7. Finally, commits those changes, execute `make charts` and commit those changes in a second commit
8. Fork [rancher/rancher](https://github.com/rancher/rancher) and change the charts URL to point to your fork
9. Start Rancher locally (instructions: [Rancher Wiki](https://github.com/rancher/rancher/wiki/Setting-Up-Rancher-2.0-Development-Environment)) and your fork's chart should be deployed

## Advanced: Standalone Fleet (Obsolete How-to)

Continuous Integration executes most [E2E tests](/e2e/) against Fleet Standalone. For developing purposes we recommend using our [dev scripts](#local-development-workflow-fleet-standalone---for-running-e2e-tests) instead of this how-to. We keep this part only for documental reasons.

Build and push your `fleet-agent` (`linux-amd64` image by default), install your Fleet charts, and then replace the controller deployment with your local controller build.

```sh
(
    go fmt ./...
    REPO=$AGENT_REPO make agent-dev
    docker push $AGENT_REPO/fleet-agent:dev
    for i in cattle-fleet-system fleet-default fleet-local; do kubectl create namespace $i; done
    helm install -n cattle-fleet-system fleet-crd ./charts/fleet-crd
    helm install -n cattle-fleet-system fleet --set agentImage.repository=$AGENT_REPO/fleet-agent --set agentImage.imagePullPolicy=Always ./charts/fleet
    kubectl delete deployment -n cattle-fleet-system fleet-controller
    go run cmd/fleetcontroller/main.go
)
```

Alternatively, if the agent's code has been unchanged, you can use the latest agent instead.
We'll use the latest Git tag for this, and _assume_ it is available on DockerHub.

```sh
(
    go fmt ./...
    for i in cattle-fleet-system fleet-default fleet-local; do kubectl create namespace $i; done
    helm install -n cattle-fleet-system fleet-crd ./charts/fleet-crd
    helm install -n cattle-fleet-system fleet --set agentImage.tag=$(git tag --sort=taggerdate | tail -1) ./charts/fleet
    kubectl delete deployment -n cattle-fleet-system fleet-controller
    go run cmd/fleetcontroller/main.go
)
```

---

### Tips and Tricks

Since the Fleet components' controller framework of choice is [Wrangler](https://github.com/rancher/wrangler), we can share caches and avoid unnecessary API requests.
Moreover, we can customize enqueue logic to decrease load on the cluster and its components.
For example: if a `BundleDeployment` encounters failure and meets certain criteria such that it'll never become active, we should move the object to a permanent error state that requires manual triage.
While reconciling state and automatically attempting to reach desired state is... desired..., we should find opportunities to eliminate loops, scheduling logic, and frequent re-enqueuing so that we decrease CPU and network load.
Solving example scenario may even result in manual triage for the `BundleDeployment`, which could be a good trade-off for the user!

To examine Fleet network load, we can use Istio pod injection to monitor network traffic and observe it with Kiali.
If Istio is installed via the Rancher UI, you can perform pod injection with a checkbox per pod.
To learn more, please refer to the [Istio documentation for Rancher](https://rancher.com/docs/rancher/v2.6/en/istio/).
