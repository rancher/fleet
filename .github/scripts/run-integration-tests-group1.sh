#!/bin/bash

set -euxo pipefail

SETUP_ENVTEST_VER=${SETUP_ENVTEST_VER-v0.0.0-20221214170741-69f093833822}
ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.32}

# install and prepare setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@"$SETUP_ENVTEST_VER"
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path "$ENVTEST_K8S_VERSION")
export KUBEBUILDER_ASSETS

# Group 1: Run specific packages (adjust these based on execution time analysis)
ginkgo --github-output \
  ./integrationtests/agent/... \
  ./integrationtests/bundlereader/... \
  ./integrationtests/cli/... \
  ./integrationtests/controller/...