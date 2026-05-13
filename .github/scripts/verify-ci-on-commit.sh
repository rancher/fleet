#!/usr/bin/env bash
set -euo pipefail

# Verifies that CI passed on the tagged commit before releasing.
# For commits merged via a PR, checks that all required PR CI checks passed.
# For hotfix commits (pushed directly without a PR), checks that all
# commit-level check-runs completed successfully.
#
# Required environment variables:
#   RELEASE_TAG  - the release tag to verify
#   REPO         - the GitHub repository in "owner/repo" form
#   GH_TOKEN     - a token with pull-requests:read and checks:read

: "${RELEASE_TAG:?RELEASE_TAG is required}"
: "${REPO:?REPO is required}"
: "${GH_TOKEN:?GH_TOKEN is required}"

COMMIT_SHA=$(git rev-list -n 1 "$RELEASE_TAG")
echo "Verifying CI checks for ${RELEASE_TAG} (${COMMIT_SHA})"

PR_NUMBER=$(gh api \
    "repos/${REPO}/commits/${COMMIT_SHA}/pulls" \
    --jq "[.[] | select(.merge_commit_sha == \"${COMMIT_SHA}\")] | first | .number // empty")

if [[ -n "${PR_NUMBER:-}" ]]; then
    echo "Found PR #${PR_NUMBER}, verifying required checks..."
    gh pr checks "${PR_NUMBER}" --repo "${REPO}" --required
else
    echo "No merged PR found (hotfix), verifying all commit checks..."
    ISSUES=$(gh api \
        "repos/${REPO}/commits/${COMMIT_SHA}/check-runs" \
        --jq 'if .total_count == 0 then "no CI checks found" else ([.check_runs[] | select(.status != "completed" or (.conclusion != "success" and .conclusion != "skipped")) | .name] | join(", ")) end')

    if [[ -n "${ISSUES}" ]]; then
        echo "ERROR: ${ISSUES}" >&2
        exit 1
    fi

    echo "✓ All checks passed for ${RELEASE_TAG} (${COMMIT_SHA})"
fi
