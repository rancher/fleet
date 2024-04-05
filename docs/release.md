# Release

This section contains information on releasing Fleet.
**Please note: it may be sparse since it is only intended for maintainers.**

---

## Cherry Picking Bug Fixes To Releases

With releases happening on release branches, there are times where a bug fix needs to be handled on the `main` branch and pulled into a release that happens through a release branch.

All bug fixes should first happen on the `main` branch.

If a bug fix needs to be brought into a release, such as during the release candidate phase, it should be cherry picked from the `main` branch to the release branch via a pull request. The pull request should be prefixed with the major and minor version for the release (e.g., `[v0.4]`) to illustrate it's for a release branch.

After merge verify that the Github Action test runs for the release branch were successful.

---

## Pre-Release

1. Ensure that all modules are at their desired versions in `go.mod`
1. Ensure that all nested and external images are at their desired versions (check `charts/` as well, and you can the following [ripgrep](https://github.com/BurntSushi/ripgrep) command at the root of the repository to see all images used: `rg "repository:" -A 1 | rg "tag:" -B 1`
1. Run `go mod tidy` and `go generate` and ensure that `git status` is clean
1. Determine the tag for the next intended release (must be valid [SemVer](https://semver.org/) prepended with `v`)

## Release Candidates

1. Checkout the release branch (e.g., `release-0.4`) or create it based off of the latest `main` branch. The branch name should be the first 2 parts of the semantic version with `release-` prepended.
1. Use `git tag` and append the tag from the **Pre-Release** section with `-rcX` where `X` is an unsigned integer that starts with `1` (if `-rcX` already exists, increment `X` by one)

## Full Releases

1. Open a draft release on the GitHub releases page
1. Send draft link to maintainers with view permissions to ensure that contents are valid
1. Create GitHub release and create a new tag on the appropriate release branch while doing so (using the tag from the **Pre-Release** section)

## Post-Release

1. Pull Fleet images from DockerHub to ensure manifests work as expected
1. Open a PR in [rancher/charts](https://github.com/rancher/charts) that ensures every Fleet-related chart is using the new RC (branches and number of PRs is dependent on Rancher)


