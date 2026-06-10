#!/bin/bash

# Install k3d from a pinned release, verifying the SHA-256 checksum before
# placing the binary on PATH.
#
# renovate: datasource=github-releases depName=k3d-io/k3d
K3D_VERSION="v5.9.0"

# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.9.0
K3D_SUM_amd64="06d8f25bc3a971c4eb29e0ff08429b180402db0f4dec838c9eac427e296800a0"
# shellcheck disable=SC2034
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.9.0
K3D_SUM_arm64="03cde5cf23e6e8e67de5a039ecf26e5b85aca82fba3e5d13dadf904cd218a250"

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
