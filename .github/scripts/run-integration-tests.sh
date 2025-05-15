#!/bin/bash

set -euxo pipefail

SETUP_ENVTEST_VER=${SETUP_ENVTEST_VER-v0.0.0-20250218120612-6f6111124902}
ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.32}

# install and prepare setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@"$SETUP_ENVTEST_VER"
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path "$ENVTEST_K8S_VERSION")
export KUBEBUILDER_ASSETS

# run integration tests
ginkgo --github-output --trace ./integrationtests/...
