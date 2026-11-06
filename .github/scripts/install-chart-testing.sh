#!/bin/bash

# Install chart-testing, yamale and yamllint for linting the charts, verifying
# the release checksum before placing anything on PATH.
#
# renovate: datasource=github-releases depName=helm/chart-testing
CT_VERSION="v3.12.0"
# renovate: datasource=github-release-attachments depName=helm/chart-testing digestVersion=v3.12.0
CT_SUM_amd64="8b450caac147de6838bde818f6923324c60585295181198eeb624b22c02894f4"
# renovate: datasource=pypi depName=yamale
YAMALE_VERSION="6.1.0"
# renovate: datasource=pypi depName=yamllint
YAMLLINT_VERSION="1.38.0"

set -euo pipefail

ARCHIVE="chart-testing_${CT_VERSION#v}_linux_amd64.tar.gz"
VENV="${HOME}/.local/share/chart-testing-venv"

TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

curl -sSfL \
  "https://github.com/helm/chart-testing/releases/download/${CT_VERSION}/${ARCHIVE}" \
  -o "${TMPDIR}/${ARCHIVE}"
echo "${CT_SUM_amd64}  ${TMPDIR}/${ARCHIVE}" | sha256sum -c -

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}"

python3 -m venv "${VENV}"
"${VENV}/bin/pip" install --quiet \
  "yamale==${YAMALE_VERSION}" "yamllint==${YAMLLINT_VERSION}"
install -m 0755 "${TMPDIR}/ct" "${VENV}/bin/ct"

# ct reads chart_schema.yaml and lintconf.yaml from $HOME/.ct by default.
mkdir -p "${HOME}/.ct"
install -m 0644 "${TMPDIR}"/etc/*.yaml "${HOME}/.ct/"

echo "Installed chart-testing ${CT_VERSION}, yamale ${YAMALE_VERSION} and yamllint ${YAMLLINT_VERSION} to ${VENV}/bin"
