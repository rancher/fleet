#!/bin/bash
#
# Verify that the pkg/apis and pkg/helmvalues modules are released in lockstep.
#
# pkg/helmvalues depends on pkg/apis and both are tagged with the same version
# from the same commit. For a given version this checks that:
#   1. Both pkg/apis/<version> and pkg/helmvalues/<version> tags exist.
#   2. Both tags point at the same commit (so helmvalues is built against the
#      exact pkg/apis it requires).
#   3. pkg/helmvalues/go.mod (as of its tag) requires pkg/apis at that same version.
#
# Usage: check-module-tag-sync.sh [<version>]
#   <version> looks like v0.16.0-beta.1. When omitted, the latest existing
#   pkg/helmvalues/* tag is used (and it is a no-op if there are none yet).

set -euo pipefail

version="${1:-}"
if [ -z "${version}" ]; then
    # Newest tag by creation date. Note: `sort -V` is not semver-aware for
    # pre-release suffixes (it orders v1.2.3 *before* v1.2.3-rc.1), so it can't
    # be used to pick the latest release here.
    version=$(git tag -l 'pkg/helmvalues/v*' --sort=-creatordate | sed 's#^pkg/helmvalues/##' | head -n1)
    if [ -z "${version}" ]; then
        echo "No pkg/helmvalues/* tags found; nothing to verify."
        exit 0
    fi
    echo "No version given; verifying latest pkg/helmvalues tag: ${version}"
fi

apis_tag="pkg/apis/${version}"
helmvalues_tag="pkg/helmvalues/${version}"

# 1. Both tags must exist.
for tag in "${helmvalues_tag}" "${apis_tag}"; do
    if ! git rev-parse -q --verify "refs/tags/${tag}" >/dev/null; then
        echo "ERROR: tag '${tag}' not found." >&2
        echo "pkg/apis and pkg/helmvalues must be tagged with the same version." >&2
        exit 1
    fi
done

# 2. Both tags must point at the same commit.
apis_commit=$(git rev-list -n1 "${apis_tag}")
helmvalues_commit=$(git rev-list -n1 "${helmvalues_tag}")
if [ "${apis_commit}" != "${helmvalues_commit}" ]; then
    {
        echo "ERROR: ${apis_tag} and ${helmvalues_tag} point at different commits:"
        echo "  ${apis_tag} -> ${apis_commit}"
        echo "  ${helmvalues_tag} -> ${helmvalues_commit}"
        echo "They must be tagged on the same commit, so helmvalues is built against"
        echo "the exact pkg/apis version it requires."
    } >&2
    exit 1
fi

# 3. helmvalues go.mod (as of its tag) must require pkg/apis at the same version.
required=$(git show "${helmvalues_tag}:pkg/helmvalues/go.mod" \
    | grep -oE 'github.com/rancher/fleet/pkg/apis[[:space:]]+v[0-9][^[:space:]]*' \
    | awk '{print $NF}' | head -n1 || true)

if [ -z "${required}" ]; then
    echo "ERROR: pkg/helmvalues/go.mod at ${helmvalues_tag} has no 'require' for github.com/rancher/fleet/pkg/apis" >&2
    exit 1
fi

if [ "${required}" != "${version}" ]; then
    {
        echo "ERROR: version mismatch between the tag and pkg/helmvalues/go.mod:"
        echo "  tag version:           ${version}"
        echo "  pkg/helmvalues/go.mod: requires pkg/apis ${required}"
        echo "Bump the require to ${version} before tagging."
    } >&2
    exit 1
fi

echo "OK: ${apis_tag} and ${helmvalues_tag} are in sync (commit ${helmvalues_commit});"
echo "    pkg/helmvalues/go.mod requires pkg/apis ${version}."
