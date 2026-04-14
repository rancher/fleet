#!/bin/bash

target_branch=$1
new_fleet=$2
new_chart=$3
repo=$4

# Check if the environment variable is set
if [ -z "$GITHUB_TOKEN" ]; then
    echo "Environment variable GITHUB_TOKEN is not set."
    exit 1
fi

# Configure git login
gh auth setup-git

# Create and push new branch
git remote add fork "https://github.com/rancherbot/$repo"
BRANCH_NAME="fleet-$(date +%s)"
git checkout -b "$BRANCH_NAME"
git push fork "$BRANCH_NAME"

# Create a pull request
gh pr create --title "[${target_branch}] fleet ${new_chart}+up${new_fleet} update" \
             --body "Update Fleet to v${new_fleet}"$'\n\n'"Changelog: https://github.com/rancher/fleet/releases/tag/v${new_fleet}" \
             --base "${target_branch}" \
             --repo "rancher/$repo" --head "rancherbot:$BRANCH_NAME"