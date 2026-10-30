# Contributing to Fleet

Fleet accepts contributions via GitHub issues and pull requests.
This document outlines the process to get your pull request accepted.

Fleet does accept external contributions in general, however the team's time is limited. Fleet is on a quarterly release cycle, so it might take a long time for external contributions to be commented on, let alone reviewed and merged. We expect review times to improve in the future.

## Genuine Investment in Fleet

Maintainer time and project resources are limited. We only accept pull requests from contributors who use Fleet or are genuinely invested and willing to follow through on their contributions. This applies whether or not AI tools are used. New contributors are welcome; no prior contribution history is required.

Explain how your changes benefit Fleet, respond to review feedback, and stay involved after merge to help address questions or regressions related to your changes. The goal is lasting collaboration, not submissions that leave maintainers with all the follow-up work.

We do not accept pull requests whose purpose is to collect points, badges, contribution counts, or other rewards rather than contribute meaningfully to Fleet. This includes mass-producing AI-generated pull requests across many projects without a genuine interest in those projects. Contributing to multiple projects or using AI is not, by itself, grounds for rejection.

Maintainers may close low-effort pull requests or those that do not meet our contribution policies without a detailed technical review. For AI-assisted contributions, also follow our [AI Contribution Policy](AI_POLICY.md).

## Start With An Issue

Prior to creating a pull request it is a good idea to [create an issue].
This is especially true if the change request is something large.
The bug, feature request, or other type of issue can be discussed prior to
creating the pull request. This can reduce rework.

[Create an issue]: https://github.com/rancher/fleet/issues/new

## Pull Requests

Pull requests for a code change should reference the issue they are related to.
This will enable issues to serve as a central point of reference for a change.
For example, if a pull request fixes or completes an issue the commit or
pull request should include:

```md
Refers #123
```

In this case 123 is the corresponding issue number.

We leave issues open, until the quality assurance team reviewed them.

## Semantic Versioning

Fleet follows [semantic versioning](https://semver.org/).
While Fleet is still on 0.y.z, we stay backwards compatible and avoid breaking changes. We increase the minor version when new features are added.

This does not cover other tools included in Fleet.

## Coding Style

Fleet expects its Go code to be formatted with `goimports`.

Imports are organized in groups, with blank lines between them. The standard
library packages are always in the first group, followed by a group for github
imports, a separate group for Fleet's own modules and groups for rancher and
k8s. Dot imports are a separate group, e.g. in tests.
If in doubt follow the existing style in the package.


Fleet further follows the style guidelines at

  - [Effective Go](https://go.dev/doc/effective_go) and
  - [Go Wiki Code Review Comments](https://go.dev/wiki/CodeReviewComments)
  - [Go Style At Google](https://google.github.io/styleguide/go/guide)

The used linters are configured in [.golangci.json](https://github.com/rancher/fleet/blob/main/.golangci.json).
