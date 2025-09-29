#!/usr/bin/env bash

set -euo pipefail

CRDS_YAML=${1?-The path to the charts.yaml file to be patched must be specified}
shift

tmpdir=$(mktemp -d)
trap 'rm -rf ${tmpdir}' EXIT

log() {
  echo "$*" >&2
}

run_yq() {
  # Use the Python-based version of yq. The Go version does not have complete support for jq's syntax
  if ! yq --help | grep -q github.com/kislyuk/yq ; then
    image="fleet-codegen-hack:yq"
    log "yq (from https://github.com/kislyuk/yq) is missing, building a helper docker image ($image)..."

    docker build -t $image - >&2 << EOF
FROM registry.suse.com/bci/python:3.11

RUN zypper in -y jq
RUN python3 -m pip install yq
EOF
    docker run --rm -i -v ${PWD}:${PWD} -w ${PWD} ${image} yq $@
  else
    yq $@
  fi
}

# Ensure the right version of controller-gen is installed
CONTROLLERGEN=controller-gen
CONTROLLERGEN_VERSION=$(go list -m -f '{{.Version}}' sigs.k8s.io/controller-tools)
if ! $CONTROLLERGEN --version | grep -q "${CONTROLLERGEN_VERSION}" ; then
  log "Downloading controller-gen ${CONTROLLERGEN_VERSION} to a temporary directory. Run 'go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLERGEN_VERSION}' to get a persistent installation"
  GOBIN="${tmpdir}/bin" go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLERGEN_VERSION}
  CONTROLLERGEN="${tmpdir}/bin/controller-gen"
fi

# Run controller-gen
${CONTROLLERGEN} object:headerFile=cmd/codegen/boilerplate.go.txt,year="$(date +%Y)" paths="./pkg/apis/..."
${CONTROLLERGEN} crd webhook paths="./pkg/apis/..." output:stdout > $CRDS_YAML
# Sort
run_yq --slurp --sort-keys --explicit-start --yaml-output -i 'sort_by(.metadata.name)[]' $CRDS_YAML
