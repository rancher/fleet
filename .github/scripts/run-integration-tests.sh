#!/bin/bash

set -euxo pipefail

SETUP_ENVTEST_VER=v0.0.0-20221214170741-69f093833822
ENVTEST_K8S_VERSION=1.25

# install and prepare setup-envtest
if ! command -v setup-envtest &> /dev/null
then
    go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$SETUP_ENVTEST_VER
fi
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path $ENVTEST_K8S_VERSION)
export KUBEBUILDER_ASSETS

# run integration tests
go test ./integrationtests/...
