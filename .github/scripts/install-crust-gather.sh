#!/bin/bash

# Install crust-gather from a pinned release, verifying the SHA-256 checksum
# before placing the binary on PATH.
#
# renovate: datasource=github-releases depName=crust-gather/crust-gather
CRUST_GATHER_VERSION="v0.17.1"
# Strip leading 'v' for the archive name
CRUST_GATHER_VER="${CRUST_GATHER_VERSION#v}"

# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.17.1
CRUST_GATHER_SUM_amd64="28b31a4f07072576bae7d6ac3a4f2dd27a06a79f7f04d37a8762b3629ea82283"
# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.17.1
CRUST_GATHER_SUM_arm64="daee96610b81002fa9d975e5b2ed0e4319b8c34525de9bb7a662286dd747cf1d"

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
