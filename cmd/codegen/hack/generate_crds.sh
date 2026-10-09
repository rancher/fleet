#!/usr/bin/env bash

set -euo pipefail

CRDS_YAML=${1?-The path to the charts.yaml file to be patched must be specified}
shift

tmpdir=$(mktemp -d)
trap 'rm -rf ${tmpdir}' EXIT

log() {
  echo "$*" >&2
}

# Ensure the right version of controller-gen is installed
CONTROLLERGEN="go tool -modfile gotools/controller-gen/go.mod controller-gen"

# Run controller-gen
${CONTROLLERGEN} object:headerFile=cmd/codegen/boilerplate.go.txt,year="$(date +%Y)" paths="./pkg/apis/..."
# controller-gen writes one file per CRD, named after its group and kind, so a
# sorted glob yields the CRDs in alphabetical order of their full names.
mkdir -p "${tmpdir}/crds"
${CONTROLLERGEN} crd webhook paths="./pkg/apis/..." output:crd:dir="${tmpdir}/crds"
cat "${tmpdir}"/crds/*.yaml > $CRDS_YAML
