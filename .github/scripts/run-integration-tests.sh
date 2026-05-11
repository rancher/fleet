#!/bin/bash

set -euxo pipefail

# renovate: datasource=go depName=sigs.k8s.io/controller-runtime
SETUP_ENVTEST_VER=${SETUP_ENVTEST_VER-v0.24.0}
ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.36}

# install and prepare setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@"$SETUP_ENVTEST_VER"
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path "$ENVTEST_K8S_VERSION")
export KUBEBUILDER_ASSETS

# run integration tests
ginkgo --github-output --trace ./integrationtests/...
