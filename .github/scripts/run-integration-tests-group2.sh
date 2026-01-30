#!/bin/bash

set -euxo pipefail

SETUP_ENVTEST_VER=${SETUP_ENVTEST_VER-v0.0.0-20251014082336-b8f11375258f}
ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.35}

# install and prepare setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@"$SETUP_ENVTEST_VER"
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path "$ENVTEST_K8S_VERSION")
export KUBEBUILDER_ASSETS

# Group 2: Run everything else - this will automatically include newly added directories
find ./integrationtests -type d -not -path '*/.git*' \
  -not -path './integrationtests/agent*' \
  -not -path './integrationtests/bundlereader*' \
  -not -path './integrationtests/cli*' \
  -not -path './integrationtests/controller*' -print0 | \
  xargs -0 ginkgo --github-output --trace