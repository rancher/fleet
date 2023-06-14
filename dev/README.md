These scripts are used for running tests locally in k3d. Don't use these on
production systems.

## Configuration

You can set these manually or put them in an `.envrc`:

    # use fleet-default for fleet in Rancher, fleet-local for standalone
    export FLEET_E2E_NS=fleet-local

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

    # needed for gitrepo tests
    #export FORCE_GIT_SERVER_BUILD="yes" # set to an empty value to skip rebuilds
    #export GIT_REPO_USER="git"
    #export GIT_REPO_URL="git@github.com:yourprivate/repo.git"
    #export GIT_REPO_HOST="github.com"
    #export GIT_SSH_KEY="$HOME/.ssh/id_ecdsa_test"
    #export GIT_SSH_PUBKEY="$HOME/.ssh/id_ecdsa_test.pub"
    #export GIT_HTTP_USER="fleet-ci"
    #export GIT_HTTP_PASSWORD="foo"

    # needed for OCI tests
    export CI_OCI_USERNAME="fleet-ci"
    export CI_OCI_PASSWORD="foo"
    export CI_OCI_CERTS_DIR="../../FleetCI-RootCA"

## Running Tests on K3D

This should set up k3d, and the fleet standalone images for single cluster tests

    export FLEET_E2E_NS=fleet-local FLEET_E2E_CLUSTER=k3d-upstream
    export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-upstream
    dev/setup-k3d
    dev/build-fleet
    dev/import-images-k3d
    dev/setup-fleet
    dev/create-zot-certs 'FleetCI-RootCA' # for OCI tests (see additional instructions [here](#running-tests-involving-an-oci-registry))
    ginkgo e2e/single-cluster

Optional flags for reporting on long-running tests: `--poll-progress-after=10s --poll-progress-interval=10s`.

For multi-cluster tests we need to configure two clusters. You also need to
make the upstream clusters API accessible to the downstream cluster. The
default `url` in [dev/setup-fleet-downstream] should work with most systems.

    export FLEET_E2E_NS=fleet-local FLEET_E2E_CLUSTER=k3d-upstream
    export FLEET_E2E_CLUSTER_DOWNSTREAM=k3d-downstream
    dev/setup-k3ds
    dev/build-fleet
    dev/import-images-k3d
    dev/setup-fleet-multi-cluster
    ginkgo e2e/multi-cluster

To test changes incrementally, rebuild just one binary, update the image in k3d
and restart the controller:

    dev/update-agent-k3d
    dev/update-controller-k3d

### Running tests involving an OCI registry

The root CA certificate created via `dev/create-zot-certs` will need to be added to the host's trusted certs; refer to
your host OS' guidelines for this. For instance, on openSUSE this can be done via:
```
sudo cp <path>/<cert_name>.crt /etc/pki/trust/anchors
```

Then, a Helm chart must be packaged to be used by OCI tests:
```
helm package e2e/assets/gitrepo/sleeper-chart/ # creates sleeper-chart-0.1.0.tgz
```

## Different Script Folders

Our CIs, dapper/drone and github actions, use a different set of scripts.
CI does not reuse dev scripts, however dev scripts may use CI scripts.
We want to keep CI scripts short, targeted and readable. Dev scripts may
change in an incompatible way anyday.

## Requirements

* docker
* git
* go
* helm
* jq
* k3d
* kubectl
* ...

## Run integration tests

```
./dev/run-integration-tests.sh
```

This will download and prepare setup-envtest, then it will execute all the integration tests.
