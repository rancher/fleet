#!/usr/bin/env bash

set -euo pipefail

tmpdir=$(mktemp -d)
trap 'rm -rf ${tmpdir}' EXIT

log() {
  echo "$*" >&2
}


# Ensure the latest version of controller-gen is installed
CONTROLLERGEN=controller-gen
CONTROLLERGEN_VERSION=$(curl -sL https://api.github.com/repos/kubernetes-sigs/controller-tools/releases/latest | jq -r ".tag_name")
if ! $CONTROLLERGEN --version | grep -q "${CONTROLLERGEN_VERSION}" ; then
  log "Downloading controller-gen ${CONTROLLERGEN_VERSION} to a temporary directory. Run 'go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLERGEN_VERSION}' to get a persistent installation"
  GOBIN="${tmpdir}/bin" go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLERGEN_VERSION}
  CONTROLLERGEN="${tmpdir}/bin/controller-gen"
fi

# Run controller-gen
${CONTROLLERGEN} object:headerFile=scripts/boilerplate.go.txt,year="$(date +%Y)" paths="./pkg/apis/..."
${CONTROLLERGEN} crd webhook paths="./pkg/apis/..." output:stdout > chart/crds/crds.yaml