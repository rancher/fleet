# Dev Scripts

These scripts are used for running tests locally in k3d. Don't use these on
production systems.

## Requirements

- docker
- git
- go
- helm
- jq
- k3d
- kubectl
- ...

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

# running multi-cluster tests in k3d (setup-k3ds)
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
### Troubleshooting

If running the `infra setup` script returns an error about flag
`--insecure-skip-tls-verify` not being found, check which version of Helm you
are using via `helm version`. In case you have Rancher Desktop installed, you
may be using its own Helm fork from `~/.rd/bin` by default, based on a different
version of upstream Helm. Feel free to set environment variable `HELM_PATH` to
remedy this. By default, the setup script will use `/usr/bin/helm`.

## Different Script Folders

Our CIs, dapper/drone and github actions, use a different set of scripts. CI
does not reuse dev scripts, however dev scripts may use CI scripts. We want to
keep CI scripts short, targeted and readable. Dev scripts may change in an
incompatible way at any day.

## Run integration tests

```bash
./dev/run-integration-tests.sh
```

This will download and prepare setup-envtest, then it will execute all the
integration tests.

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

RUN apt update && apt upgrade -y
RUN apt install -y wget curl git jq nodejs

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
    "dns": [
        "1.1.1.1"
    ]
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

#### Using `act` fails