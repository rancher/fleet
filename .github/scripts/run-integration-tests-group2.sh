#!/bin/bash

set -euxo pipefail

SETUP_ENVTEST_VER=${SETUP_ENVTEST_VER-v0.0.0-20251014082336-b8f11375258f}
ENVTEST_K8S_VERSION=${ENVTEST_K8S_VERSION-1.35}

# install and prepare setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@"$SETUP_ENVTEST_VER"
KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path "$ENVTEST_K8S_VERSION")
export KUBEBUILDER_ASSETS

# Group 2: Run everything not in group 1; auto-discovers new test packages.
# Uses go list to find packages with test files, skipping asset dirs and
# helper-only packages (e.g. utils) that have no test suite.
mapfile -t group2_packages < <(
  go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./integrationtests/... \
    | grep -Ev '/integrationtests/(agent|cli|controller)(/|$)' \
    | sed 's|github.com/rancher/fleet/|./|' \
    || true
)

if [ "${#group2_packages[@]}" -eq 0 ]; then
  echo "No group 2 integration test packages found."
  exit 0
fi

ginkgo --github-output --trace "${group2_packages[@]}"
