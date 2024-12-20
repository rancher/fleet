# Logging

Instructions on writing useful, well-formatted log messages can be found [here](./LOGGING.md).

# Testing: Dev Scripts

These scripts are used for running tests locally in [k3d](https://github.com/rancher/k3d).
Don't use these on production systems.

## Requirements

- docker
- git
- go
- helm
- jq
- k3d
- kubectl
- ...

### For Mac users

On Mac OS, Docker runs in a virtual machine. This means the network setup is more complicated.

> If you are unsure about which method you would like use for tunneling to localhost, we recommend [ngrok](https://ngrok.com).

More info [here](https://github.com/rancher/rancher/wiki/Setting-Up-Rancher-Development-Environment#ngrok).

## Running Tests on K3D

These commands should set up k3d and the fleet standalone images for single
cluster tests and run those.

```bash
source dev/setup-single-cluster
ginkgo e2e/single-cluster
```

Optional flags to `ginkgo` for reporting on long-running tests:
`--poll-progress-after=10s --poll-progress-interval=10s`.

For multi-cluster tests we need to configure two clusters. You also need to make
the upstream clusters API accessible to the downstream cluster. The default URL
in `dev/setup-fleet-downstream` should work with most systems.

```bash
source dev/setup-multi-cluster
ginkgo e2e/multi-cluster
```

### Testing changes incrementally

To test changes incrementally, rebuild just one binary, update the image in k3d
and restart the controller. Make sure you have sourced the right configuration
for the current setup.

```bash
dev/update-agent-k3d
dev/update-controller-k3d
```

## Running tests in Rancher

Any Fleet commit can be tested against the latest Rancher release, by:

1. Running `./.github/workflows/release-against-test-charts.yml`, which will
create Fleet charts based on the chosen git ref, and store them in the specified
repo and branch, with version `999.9.9+up9.9.9` to prevent collisions with any
other existing Fleet chart versions.

2. Setting the charts branch environment variable:
```
export CHARTS_BRANCH=<branch_created_in_previous_step>
```
3. Ensuring you have access to a running cluster in which to install Rancher.
For instance, a set of k3d clusters (1 upstream + 1 downstream) can be created
on your local machine through:

```
dev/setup-k3d
dev/setup-k3ds-downstream
```

4. Commenting out the pairs of lines (name + value) setting the following
values for installing Rancher through Helm in script
`dev/setup-rancher-with-dev-fleet`, to match the location and version of the
Fleet build generated in step 1:
* `--set extraEnv[0].name=CATTLE_FLEET_VERSION \` to use a custom Fleet version,
otherwise Rancher will use the version pinned in its
[settings](https://github.com/rancher/rancher/blob/main/build.yaml#L5).
* `--set extraEnv[0].value=999.9.9+up9.9.9 \` to match the version from step 1.
* `--set extraEnv[1].name=CATTLE_CHART_DEFAULT_URL \` to set a custom chart URL
* `--set extraEnv[1].value=https://github.com/fleetrepoci/charts \`: this is the
custom charts repository used in step 1 by default.
Change it to a different URL if you have specified one in that step.
* `--set extraEnv[2].name=CATTLE_CHART_DEFAULT_BRANCH \` to set a custom chart
repo branch.
* `--set extraEnv[2].value=$branch \`: this must match the branch name created
in step 1.
* `--set rancherImageTag=$tag \`: only needed for custom Rancher builds.
If you have not built Rancher yourself, this is typically not needed and you
can leave it commented out.

5. Running script `dev/setup-rancher-with-dev-fleet`, which will install Rancher
using Helm, including test Fleet charts generated earlier. Once it is done,
single- or multi-cluster test suites can be run against an actual Rancher setup.

This also enables use of the Rancher UI, after opening the URL output by Helm
when installing the Rancher chart, then logging in using the `bootstrapPassword`
set in that script.

## Configuration

### Running scripts manually

You can set these environment variables for configuration manually, but it is
advised to put them in `.envrc` and source them before running any of the
scripts (except for `dev/setup-{single,multi}-cluster`), if the scripts are run
manually. You can rely on the environment variables being set correctly if you
source the `dev/setup-{single,multi}-cluster` scripts.

```bash
source .envrc
```

### Running setup scripts

If you use `dev/setup-single-cluster` or `dev/setup-multi-cluster` you can
simply put your custom configuration in the root of the repository as
`env.single-cluster` and `env.multi-cluster`. Those files will then be used
instead of the defaults in `dev/env.single-cluster-defaults` or
`dev/env.multi-cluster-defaults`, respectively.

If you occasionally want to specify a different file, you can set the
`FLEET_TEST_CONFIG` environment variable to point to your custom configuration
(like `.envrc`) file. This will make those scripts use the file specified in the
`FLEET_TEST_CONFIG` environment variable instead of the defaults in
`dev/env.single-cluster-defaults` and `dev/env.multi-cluster-defaults` and also
instead of the custom configuration in `env.single-cluster` and
`env.multi-cluster`.

### A list of all environment variables

```bash
# use fleet-default for fleet in Rancher, fleet-local for standalone
export FLEET_E2E_NS=fleet-local
export FLEET_E2E_NS_DOWNSTREAM=fleet-default

# running single-cluster tests in Rancher Desktop
export FLEET_E2E_CLUSTER=rancher-desktop
export FLEET_E2E_CLUSTER_DOWNSTREAM=rancher-desktop

# running single-cluster tests in k3d (setup-k3d)
export FLEET_E2E_CLUSTER=k3d-upstream
export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-upstream

# running multi-cluster tests in k3d (setup-k3d;setup-k3ds)
export FLEET_E2E_CLUSTER=k3d-upstream
export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-downstream

# for running tests on darwin/arm64
export GOARCH=arm64

# needed for gitrepo tests, which are currently disabled but part of the
# single-cluster tests
export FORCE_GIT_SERVER_BUILD="yes" # set to an empty value to skip rebuilds
export GIT_REPO_USER="git"
export GIT_REPO_URL="git@github.com:yourprivate/repo.git"
export GIT_REPO_HOST="github.com"
export GIT_SSH_KEY="$HOME/.ssh/id_ecdsa_test"
export GIT_SSH_PUBKEY="$HOME/.ssh/id_ecdsa_test.pub"
export GIT_HTTP_USER="fleet-ci"
export GIT_HTTP_PASSWORD="foo"

# needed for OCI tests, which are part of the single-cluster tests
export CI_OCI_USERNAME="fleet-ci"
export CI_OCI_PASSWORD="foo"
export CI_OCI_CERTS_DIR="../../FleetCI-RootCA"

# optional, for selecting Helm versions (see [Troubleshooting](#troubleshooting))
export HELM_PATH="/usr/bin/helm"
```

### `public_hostname`

Several scripts support the `public_hostname` configuration variable.
The variable is set to a DNS record, which points to the public interface IP of the host.
The k3d cluster is then set up with port forwardings, so that host ports are
redirected to services inside the cluster.

The default `public_hostname` is `172.18.0.1.sslip.io`, which points
to the default Docker network gateway. That gateway address might vary for
custom networks, see for example: `docker network inspect fleet -f '{{(index .IPAM.Config0).Gateway}}'`.
Careful, several internet routers provide "DNS rebind protection" and won't return an IP for `172.18.0.1.sslip.io`, unless the .sslip.io` domain is in an allow list.
Any magic wildcard DNS resolver will do, or you can create an A record in your own DNS zone.

The k3d cluster is set up with multiple port forwardings by the scripts: `-p '80:80@server:0' -p '443:443@server:0'`.
More arguments can be provided via the `k3d_args` variable.

### Troubleshooting

If running the `infra setup` script returns an error about flag
`--insecure-skip-tls-verify` not being found, check which version of Helm you
are using via `helm version`. In case you have Rancher Desktop installed, you
may be using its own Helm fork from `~/.rd/bin` by default, based on a different
version of upstream Helm. Feel free to set environment variable `HELM_PATH` to
remedy this. By default, the setup script will use `/usr/bin/helm`.

## Different Script Folders

Our CIs and github actions use a different set of scripts. CI
does not reuse dev scripts, however dev scripts may use CI scripts. We want to
keep CI scripts short, targeted and readable. Dev scripts may change in an
incompatible way at any day.

## Run Integration Tests

```bash
./dev/run-integration-tests.sh
```

This will download and prepare setup-envtest, then it will execute all the
integration tests.

## Local Infra Setup

The local infra setup creates pods for:

- git server, using nginx with git-http-backend, port 8080/tcp
- OCI repo server, using Zot, port 8081/tcp
- Helm registry, using chartmuseum, port 8082/tcp

To build and run the infra setup command do:

```
pushd e2e/testenv/infra
  go build
popd
./e2e/testenv/infra/infra setup

```

The resulting deployments use a loadbalancer service, which means the host must be able to reach the loadbalancer IP.
Therefore the infra setup doesn't work with the `public_hostname` config variable.
This is not a problem, unless k3d is running in a VM and not directly on the host.

It is possible to override the loadbalancer IP by setting the `external_ip` environment variable.

## Running Github Actions locally

Sometimes, it may be beneficial to be able to run the Github Action tests using
the same configuration which is used remotely. To do this, you can use
[nektos/act](https://github.com/nektos/act).

### Requirements

- [Docker](https://www.docker.com/)
- [act](https://github.com/nektos/act#installation)

### Installation

To install `act`, please follow the [instructions on the official Github
repository](https://github.com/nektos/act#installation).

### Configuration

#### Container Image

Unlike Github Actions, `act` will use container images for running the tests
locally. The container image that will be used depends on the type of the
Github Action Runner for the specific action. You can see which images are being
used for which runner [here](https://github.com/nektos/act#runners). Most tests require `ubuntu-latest`.

The containers are available in difference sizes:

- Micro
- Medium
- Large

The default container image for Ubuntu, which is required for most actions, does
not contain all necessary tools. They are [intentionally
incomplete](https://github.com/nektos/act#default-runners-are-intentionally-incomplete).
Instead, the large container image needs to be used (read on for an
alternative).

To change the container image used for Ubuntu, you will need to create a
configuration file `$HOME/.actrc`.

```shell
-P ubuntu-latest=catthehacker/ubuntu:full-latest
```

While the medium-sized container image has a size of only 1.1GB, the large
container image is significantly larger, approximately 40GB in size. If this is
a concern, you can create your own container image using the following
`Dockerfile`. This will result in a container image of about 700MB, while still
including all the necessary tools to run both single and multi-cluster tests of
fleet.

```Dockerfile
FROM ubuntu:22.04

ARG BUILDARCH=amd64
ARG NVM_VERSION=v0.39.7
ARG NODE_VERSION=20

RUN apt update && apt upgrade -y
RUN apt install -y wget curl git jq zstd

WORKDIR /tmp

RUN curl -fsSL -o get_docker.sh \
            https://get.docker.com && \
        chmod 700 get_docker.sh && \
        ./get_docker.sh && rm get_docker.sh

RUN wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${BUILDARCH} -O /usr/bin/yq \
        && chmod +x /usr/bin/yq

RUN curl -fsSL -o get_helm.sh \
            https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && \
        chmod 700 get_helm.sh && \
        ./get_helm.sh

RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${BUILDARCH}/kubectl" && \
        chmod +x ./kubectl && \
        mv ./kubectl /usr/local/bin/kubectl

RUN curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/${NVM_VERSION}/install.sh | bash && \
        export NVM_DIR="$HOME/.nvm" && \
        [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"  && \
        [ -s "$NVM_DIR/bash_completion" ] && \. "$NVM_DIR/bash_completion" && \
        nvm install ${NODE_VERSION} && \
        ln -s $(which node) /usr/bin/node && \
        ln -s $(which npm) /usr/bin/npm
```

Please also note, that the default behavior of `act` is to always pull images.
You can either use the `--pull=false` flag when running `act` or you will need
to upload this container image to a container registry. In any case, you need to
specify the container image to be used in the `$HOME/.actrc` file.

#### Github Token

Some tests may require a Github token to be set. While this seems a bit odd,
this can already be necessary in cases where `act` uses the Github API to fetch
repositories for a simple checkout action.

You can create a personal access token by following the [instructions on the
Github
website](https://docs.github.com/en/github/authenticating-to-github/creating-a-personal-access-token).

### Running the tests

The tests are run by simply calling `act`, **but this is not recommended**, as
it starts all available tests in parallel. Usually, you would use `act -l` to
get a list of all jobs with workflows and possible events, then choose one using
`act <event-name> -j <job-name>`. But even this can start more tests in parallel
than you may like (like this could the case for `e2e-fleet-test`, for instance).
Therefore, we recommend you use the `-W` flag instead to run a specific workflow
file.

For example:

```shell
act -W .github/workflows/e2e-multicluster-ci.yml
```

Sometimes a test has a conditions, which will prevent some tests (but not
necessarily all) from running. For instance, this is the case for the acceptance
tests, which are part of the `e2e-multicluster-ci.yml` workflow. They will only
run if the `schedule` event is passed. To also run those tests, you need to pass
the `schedule` event to `act` as such:

```shell
act schedule -W .github/workflows/e2e-multicluster-ci.yml
```

### Troubleshooting

#### fatal: not a git repository

```shell
get repo root in /: output: "fatal: not a git repository (or any of the parent directories): .git\n", error: exit status 128
```

If you see an issue like this and are running the tests from a linked git
worktree, it is likely that act has copied the contents of your linked worktree
into the container but cannot access the main worktree. Running the tests from
the main worktree instead is going to resolve this issue.

You can test this by running a simple git command like `git status` inside the
working directory of the `act` container. It should be kept running in case this
issue occurred.

```shell
docker exec -it <container> bash
git status
```

#### DNS Resolution

The DNS resolution depends on the configuration of the host system. This means
that, if the host is configured to point to itself (e.g., `127.0.1.1`), DNS
resolution might not work out-of-the-box. This is due to the use of containers
to emulate the environment of a GitHub Action. The container gets the Docker
socket passed through, but the containers created from within this container may
not be able to reach the this local DNS address of the host.

If you have such a DNS server configured on your host, which points to a local
DNS server, you can configure a separate DNS server for Docker. After that, you
will need to restart the Docker daemon. You can configure the DNS for Docker in
`/etc/docker/daemon.json`, e.g.:

```json
{
  "dns": ["1.1.1.1"]
}
```

#### Changes aren't applied

If you find yourself in the situation that changes you made to the environment
do not seem to be applied, it might be that `act` did not remove its own
container image and simply re-used it. You can remove it yourself, either by

- removing it manually using `docker rm <container>`, or
- by running `act` with the `--rm` option. This may be inconvenient, because it
  will also remove the container in case of any errors, so will not be able to
  inspect the container for issues.

This container image will have `act` in his name.

## Monitoring

This sections describes how to add a monitoring stack to your development
environment. It consists of Prometheus, kube-state-metrics, kube-operator,
Node-Exporter, Alertmanager and Grafana. It does contain Grafana dashboards and
Prometheus alerts, but it does not contain any Grafana dashboards or Prometheus
alerts specific to Fleet.

### Installation

If you have a running system, run the following commands on the upstream
cluster:

```bash
helm repo add prometheus-community \
  https://prometheus-community.github.io/helm-charts
helm repo update
helm upgrade --install --create-namespace -n cattle-system-monitoring \
  monitoring prometheus-community/kube-prometheus-stack
```

That alone suffices to get a working monitoring setup for the upstream cluster.
But to connect it to the fleet-controller exported metrics, you need to add a
service monitor for each controller.

The service monitor is currently not part of the Helm chart.  However, the
necessary Kubernetes service resource is, unless you have disabled monitoring in
the Helm chart when installing fleet.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: monitoring-fleet-controller
  namespace: cattle-fleet-system
  labels:
    release: monitoring # required to be recognized by the operator
spec:
  endpoints:
  - honorLabels: true
    path: /metrics
    scheme: http
    scrapeTimeout: 30s
    port: metrics
  jobLabel: fleet-controller
  namespaceSelector:
    matchNames:
    - cattle-fleet-system
  selector:
    matchLabels:
      app: fleet-controller
      shard-default: true
      # or:
      # shard: <shard-id>
```

This configures Prometheus to scrape the metrics from the (unsharded)
fleet-controller. Should you have sharding enabled, you will additionally need
to add one ServiceMonitor for each shard.

By accessing the Prometheus UI, you can now see the metrics from the
fleet-controller. They are all prefixed with `fleet_`, which you will need to
enter into the expression field of the Graph page to get auto-completion.
Alternatively you can use the metrics explorer, which is a button next to the
evaluation input field.

> **__NOTE:__** The Prometheus UI can be used to check the Prometheus
configuration (e.g., the scrape targets), scraped metrics, check alerts or
draft them with PromQL or to create PromQL queries to be used in Grafana
dashboards. It is not accessible by default and not meant to be shown to
casual users.

To access the Prometheus UI, you can forward the port of the Prometheus service
to your local machine and access it through that port.

```bash
kubectl port-forward -n cattle-system-monitoring \
  svc/monitoring-kube-prometheus-prometheus 9090:9090
```

Alternatively, you can forward the port of the fleet-controller to your local
machine. Then you can access the raw metrics at `http://localhost:8080/metrics`.

```bash
kubectl port-forward -n cattle-fleet-system \
  svc/monitoring-fleet-controller 8080:8080
```

### Metrics

There are metrics which will only be available when certain resources are
created. To create those resources, you can use the following file. Since a
`GitRepo` resource results in having `Bundle` and `BundleDeployment` resources,
the `Cluster` resource is already available and the `ClusterGroup` resource is
created by us, it is sufficient to create a `GitRepo` and `ClusterGroup` resource
to see all the fleet specific metrics.

```yaml
kind: GitRepo
apiVersion: fleet.cattle.io/v1alpha1
metadata:
  name: simple
  namespace: fleet-local
spec:
  repo: https://github.com/rancher/fleet-examples
  paths:
  - simple
---
kind: ClusterGroup
apiVersion: fleet.cattle.io/v1alpha1
metadata:
  name: local-group
  namespace: fleet-local
spec:
  selector:
    matchLabels:
      name: local
```
