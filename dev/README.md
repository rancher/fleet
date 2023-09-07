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

    source dev/setup-single-cluster
    ginkgo e2e/single-cluster

Optional flags to `ginkgo` for reporting on long-running tests:
`--poll-progress-after=10s --poll-progress-interval=10s`.

For multi-cluster tests we need to configure two clusters. You also need to make
the upstream clusters API accessible to the downstream cluster. The default URL
in `dev/setup-fleet-downstream` should work with most systems.

    source dev/setup-multi-cluster
    ginkgo e2e/multi-cluster

### Testing changes incrementally

To test changes incrementally, rebuild just one binary, update the image in k3d
and restart the controller. Make sure you have sourced the right configuration
for the current setup.

    dev/update-agent-k3d
    dev/update-controller-k3d

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

    # use fleet-default for fleet in Rancher, fleet-local for standalone
    export FLEET_E2E_NS=fleet-local
    export FLEET_E2E_NS_DOWNSTREAM=fleet-default

    # running single-cluster tests in Rancher Desktop
    #export FLEET_E2E_CLUSTER=rancher-desktop
    #export FLEET_E2E_CLUSTER_DOWNSTREAM=rancher-desktop

    # running single-cluster tests in k3d (setup-k3d)
    #export FLEET_E2E_CLUSTER=k3d-upstream
    #export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-upstream

    # running multi-cluster tests in k3d (setup-k3ds)
    #export FLEET_E2E_CLUSTER=k3d-upstream
    #export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-downstream

    # for running tests on darwin/arm64
    #export GOARCH=arm64

    # needed for gitrepo tests, which are currently disabled but part of the
    # single-cluster tests

    #export FORCE_GIT_SERVER_BUILD="yes" # set to an empty value to skip rebuilds
    #export GIT_REPO_USER="git"
    #export GIT_REPO_URL="git@github.com:yourprivate/repo.git"
    #export GIT_REPO_HOST="github.com"
    #export GIT_SSH_KEY="$HOME/.ssh/id_ecdsa_test"
    #export GIT_SSH_PUBKEY="$HOME/.ssh/id_ecdsa_test.pub"
    #export GIT_HTTP_USER="fleet-ci"
    #export GIT_HTTP_PASSWORD="foo"

    # needed for OCI tests, which are part of the single-cluster tests

    #export CI_OCI_USERNAME="fleet-ci"
    #export CI_OCI_PASSWORD="foo"
    #export CI_OCI_CERTS_DIR="../../FleetCI-RootCA"

    # optional, for selecting Helm versions (see [Troubleshooting](#troubleshooting))
    #export HELM_PATH="/usr/bin/helm"

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

    ./dev/run-integration-tests.sh

This will download and prepare setup-envtest, then it will execute all the
integration tests.
