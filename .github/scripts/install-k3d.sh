#!/bin/bash

# Install k3d from a pinned release, verifying the SHA-256 checksum before
# placing the binary on PATH.
#
# renovate: datasource=github-releases depName=k3d-io/k3d
K3D_VERSION="v5.8.3"

# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.8.3
K3D_SUM_amd64="dbaa79a76ace7f4ca230a1ff41dc7d8a5036a8ad0309e9c54f9bf3836dbe853e"
# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.8.3
K3D_SUM_arm64="0b8110f2229631af7402fb828259330985918b08fefd38b7f1b788a1c8687216"

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

curl -sSfL \
  "https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/k3d-linux-${ARCH}" \
  -o "${TMPDIR}/k3d"

K3D_SUM_VAR="K3D_SUM_${ARCH}"
echo "${!K3D_SUM_VAR}  ${TMPDIR}/k3d" | sha256sum -c -

install -m 0755 "${TMPDIR}/k3d" "${DEST}/k3d"
echo "Installed k3d ${K3D_VERSION} (${ARCH}) to ${DEST}/k3d"
