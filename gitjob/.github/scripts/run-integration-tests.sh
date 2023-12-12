#!/bin/bash

set -euxo pipefail

ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.25}

# install and prepare setup-envtest
if ! command -v setup-envtest &> /dev/null
then
    go install sigs.k8s.io/controller-runtime/tools/setup-envtest
fi
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path $ENVTEST_K8S_VERSION)
export KUBEBUILDER_ASSETS

# run integration tests
go test -v  ./integrationtests/...
