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
FROM bitnami/python:3.10

RUN install_packages jq
RUN python -m pip install yq
EOF
    container_id=$(docker create --rm -i -v ${PWD}:${PWD}:ro -w ${PWD} -v ${tmpdir}:${tmpdir} ${image} yq $@ )
    if [ -n "${COPY_FILE}" ] ; then
      # When running on CI, docker-in-docker may be used, so the generated input file, which is outside the working directory, is not mounted and not available
      docker cp "${COPY_FILE}" "${container_id}:${COPY_FILE}"
    fi
    docker start -ai "${container_id}"
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
CONTROLLERGEN_CRDS_YAML="${tmpdir}/crds-controllergen.yaml"
${CONTROLLERGEN} crd webhook paths="./pkg/apis/..." output:stdout > "${CONTROLLERGEN_CRDS_YAML}"

# Patch existing CRDs with the descriptions from controller-gen
PATCHED_CRDS_YAML="${tmpdir}/crds-patched.yaml"
COPY_FILE="${CONTROLLERGEN_CRDS_YAML}" run_yq -f cmd/codegen/hack/patch_crd_descriptions.jq "${CRDS_YAML}" "${CONTROLLERGEN_CRDS_YAML}" \
  --slurp --sort-keys --explicit-start --yaml-output > "${PATCHED_CRDS_YAML}"

# Override previous CRDs file
cp "${PATCHED_CRDS_YAML}" "${CRDS_YAML}"
