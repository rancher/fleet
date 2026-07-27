#!/bin/bash

# Install crust-gather from a pinned release, verifying the SHA-256 checksum
# before placing the binary on PATH.
#
# renovate: datasource=github-releases depName=crust-gather/crust-gather
CRUST_GATHER_VERSION="v0.16.0"
# Strip leading 'v' for the archive name
CRUST_GATHER_VER="${CRUST_GATHER_VERSION#v}"

# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.16.0
CRUST_GATHER_SUM_amd64="aa8bcad2331b4d85a5476f43e80e02de93270dd1c2a1cf88c44e4f63ae993b83"
# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.16.0
CRUST_GATHER_SUM_arm64="8f1b59cf2e464af104a72e95da0edc9b55de279a18a3553320b1a1ba3b6f81ac"

set -euo pipefail

ARCH=$(uname -m)
case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

DEST="${INSTALL_DIR:-${HOME}/.local/bin}"
mkdir -p "${DEST}"
TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

ARCHIVE="kubectl-crust-gather_${CRUST_GATHER_VER}_linux_${ARCH}.tar.gz"
curl -sSfL \
  "https://github.com/crust-gather/crust-gather/releases/download/${CRUST_GATHER_VERSION}/${ARCHIVE}" \
  -o "${TMPDIR}/${ARCHIVE}"

SUM_VAR="CRUST_GATHER_SUM_${ARCH}"
echo "${!SUM_VAR}  ${TMPDIR}/${ARCHIVE}" | sha256sum -c -

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}" kubectl-crust-gather
install -m 0755 "${TMPDIR}/kubectl-crust-gather" "${DEST}/crust-gather"
echo "Installed crust-gather ${CRUST_GATHER_VERSION} (${ARCH}) to ${DEST}/crust-gather"
