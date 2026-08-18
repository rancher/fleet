#!/bin/bash

# Install crust-gather from a pinned release, verifying the SHA-256 checksum
# before placing the binary on PATH.
#
# renovate: datasource=github-releases depName=crust-gather/crust-gather
CRUST_GATHER_VERSION="v0.16.3"
# Strip leading 'v' for the archive name
CRUST_GATHER_VER="${CRUST_GATHER_VERSION#v}"

# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.16.3
CRUST_GATHER_SUM_amd64="f103b61bb5aeb55c2d62d52e7a1b2719fdd0115ed1eb5331604842fb1e7e605c"
# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=crust-gather/crust-gather digestVersion=v0.16.3
CRUST_GATHER_SUM_arm64="ae697fa41c6666dbe5d059a990baf0d05427dfcd643f9477cac469bd40ad845e"

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
