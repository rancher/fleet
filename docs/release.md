# Release

This section contains information on releasing Fleet.
**Please note: it may be sparse since it is only intended for maintainers.**

---

## Updating Fleet Components

1. Update and tag [rancher/build-tekton](https://github.com/rancher/build-tekton)
1. Update the `rancher/tekton-utils` tag in the `GitJob` helm chart in [rancher/gitjob](https://github.com/rancher/gitjob)
1. Update and tag [rancher/gitjob](https://github.com/rancher/gitjob)
1. Copy the `GitJob` helm chart to `./charts/fleet/charts/gitjob` in [rancher/fleet](https://github.com/rancher/fleet)
1. Generate the `GitJob` CRD into a file in [rancher/fleet](https://github.com/rancher/fleet): `go run $GITJOB_REPO/pkg/crdgen/main.go > $FLEET_REPO/charts/fleet-crd/templates/gitjobs-crds.yaml`
1. Update and tag [rancher/fleet](https://github.com/rancher/fleet) (usually as a release candidate) to use those components in a released version of Fleet

---

## Cherry Picking Bug Fixes To Releases

With releases happening on release branches, there are times where a bug fix needs to be handled on the `master` branch and pulled into a release that happens through a release branch.

All bug fixes should first happen on the `master` branch.

If a bug fix needs to be brought into a release, such as during the release candidate phase, it should be cherry picked from the `master` branch to the release branch via a pull request. The pull request should be prefixed with the major and minor version for the release (e.g., `[v0.4]`) to illustrate it's for a release branch.

After merge verify that the Github Action test runs for the release branch were successful.

---

## Pre-Release

1. Ensure that all modules are at their desired versions in `go.mod`
1. Ensure that all nested and external images are at their desired versions (check `charts/` as well, and you can the following [ripgrep](https://github.com/BurntSushi/ripgrep) command at the root of the repository to see all images used: `rg "repository:" -A 1 | rg "tag:" -B 1`
1. Run `go mod tidy` and `go generate` and ensure that `git status` is clean
1. Determine the tag for the next intended release (must be valid [SemVer](https://semver.org/) prepended with `v`)

## Release Candidates

1. Checkout the release branch (e.g., `release-0.4`) or create it based off of the latest `master` branch. The branch name should be the first 2 parts of the semantic version with `release-` prepended.
1. Use `git tag` and append the tag from the **Pre-Release** section with `-rcX` where `X` is an unsigned integer that starts with `1` (if `-rcX` already exists, increment `X` by one)

## Full Releases

1. Open a draft release on the GitHub releases page
1. Send draft link to maintainers with view permissions to ensure that contents are valid
1. Create GitHub release and create a new tag on the appropriate release branch while doing so (using the tag from the **Pre-Release** section)

## Post-Release

1. Pull Fleet images from DockerHub to ensure manifests work as expected
1. Open a PR in [rancher/charts](https://github.com/rancher/charts) that ensures every Fleet-related chart is using the new RC (branches and number of PRs is dependent on Rancher)


