# Release

This section contains information on releasing Fleet.
**Please note: it may be sparse since it is only intended for maintainers.**

---

## Cherry Picking Bug Fixes To Releases

With releases happening on release branches, there are times where a bug fix needs to be handled on the `main` branch and pulled into a release that happens through a release branch.

All bug fixes should first happen on the `main` branch.

If a bug fix needs to be brought into a release, such as during the release candidate phase, it should be cherry picked from the `main` branch to the release branch via a pull request. The pull request should be prefixed with the major and minor version for the release (e.g., `[v0.4]`) to illustrate it's for a release branch.

After merge verify that the Github Action test runs for the release branch were successful.

## When do we branch?

We branch the next release branch release/v0.x from master only, when we start on 0.x+1 features. This should keep the distance between both branches to a minimum.

We have to make sure all the QA relevant commits are part of the release plan and their issues have the correct milestone, etc.

```
% git merge-base master release/v0.6                                                                  
2312ff8f8823320629769f9ab408472ed58c2442
% git log 2312ff8f8823320629769f9ab408472ed58c2442..master
```

After branching, we cherry pick PRs with separate issues for QA. The issues should use '[v0.x]' in their title.

## What else to do after a release

More detailed instructions, e.g. how to use the release workflows and interact with the rancher/charts repo are in the Wiki.

* generate release notes, make sure all changes are included since last release
* edit release notes, only user issues that are relevant to users, fix spelling, capitalization
* update versioned docs in fleet-docs with yarn
* adapt CI so scheduled test are run for new version
