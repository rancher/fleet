#!/bin/bash
#
# Creates or deletes the registry for the image scan E2E tests. Run "create"
# before creating the k3d cluster with "--registry-use $IMAGESCAN_REGISTRY_PULL"
# and eval its output.

set -euo pipefail

# ".localhost" makes clients use plain HTTP. This host pushes via localhost,
# pods pull via the k3d-prefixed name.
name="imagescan.localhost"
node="k3d-${name}"

case "${1-}" in
create)
    if ! docker inspect "${node}" >/dev/null 2>&1; then
        # k3d writes to stdout, which callers eval.
        k3d registry create "${name}" --port 5111 >&2
    fi

    for var in "IMAGESCAN_REGISTRY_PULL=${node}:5000" "IMAGESCAN_REGISTRY_PUSH=localhost:5111"; do
        echo "export ${var}"
        if [ -n "${GITHUB_ENV:-}" ]; then
            echo "${var}" >>"${GITHUB_ENV}"
        fi
    done
    ;;
delete)
    if docker inspect "${node}" >/dev/null 2>&1; then
        k3d registry delete "${node}"
    fi
    ;;
*)
    echo "Usage: $0 create|delete" >&2
    exit 1
    ;;
esac
