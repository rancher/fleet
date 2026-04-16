#!/usr/bin/env bash
set -euo pipefail

TAG="${1:?TAG argument required}"
VERSION="${2:?VERSION argument required}"

rm -rf .charts
mkdir .charts

# Skip chart packaging for hotfix releases
if [[ "${IS_HOTFIX:-false}" == "true" ]]; then
    exit 0
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

find charts -maxdepth 1 -mindepth 1 -type d -exec cp -R {} "${TMP_DIR}/" \;

# Update image tags in all chart values files
find "${TMP_DIR}" -maxdepth 2 -name "values.yaml" -exec sed -i.bak \
  -e "s@repository: rancher/\(fleet[-a-z]*\).*@repository: rancher/\1@" \
  -e "s@tag: dev@tag: ${TAG}@" \
  {} \;
find "${TMP_DIR}" -name "*.bak" -delete

while IFS= read -r chart_dir; do
  helm package \
    --version="${VERSION}" \
    --app-version="${VERSION}" \
    -d .charts \
    "${chart_dir}"
done < <(find "${TMP_DIR}" -maxdepth 1 -mindepth 1 -type d | sort)
