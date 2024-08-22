# Developing

This document contains tips, workflows, and more for local development within this repository.

More documentation for maintainers and developers of Fleet can be found in [docs/](docs/).

## Recommendations on writing tests

Fleet's test suites should be as reliable as possible, as they are a pillar of Fleet's quality.

Flaky tests cause delays and confusion; here are a few recommendations which can help detect and prevent them.

With many of Fleet's tests relying on [ginkgo](https://onsi.github.io/ginkgo/), suites can be run repeatedly to identify
flaky tests locally, by using `ginkgo --until-it-fails $path`. Flag `-v` adds verbosity to test runs, outputting precious
clues on what may be failing.

Making tests as lightweight as possible is a good first step towards making them less likely to be flaky, as it reduces
complexity and makes them easier to set up and tear down. This includes using unit tests where possible, especially when
many different combinations of similar logic must be tested. If unit tests cannot cover a given case, we recommend
integration tests instead, which are still fairly easy to debug and flexible with mocking.
End-to-end tests should be the last resort, as they spin up full-blown clusters, sometimes with additional test
infrastructure.

When using end-to-end tests, we recommend:

- Deploying the smallest possible amount of resources to speed up deployments and reduce the load on resources. For
instance, using [test charts](https://github.com/rancher/fleet-test-data/tree/master/simple-chart) containing only
config maps, since much of Fleet's end-to-end testing is independent of the workload itself, but rather focuses on
status changes, readiness, etc.

- Randomising namespaces whenever possible, to reduce the risk of conflicts between multiple runs of the same test
suite. The same applies to randomising release names and deployed resource names.

- Cleaning up created resources after execution, leaving a clean slate for further testing. This makes conflicts less
likely and reduces the load on the machine running tests. Pre-execution cleanup could also be a good option, ensuring
that testing only begins if cleanup is successful. [Gomega](http://github.com/onsi/gomega)'s `BeforeEach` and
`AfterEach` are our good friends there.

- When waiting for a (set of) condition(s) to be met, not using `time.Sleep(<duration>)` but rather Gomega's
[Eventually](https://onsi.github.io/gomega/#making-asynchronous-assertions). As functions can be passed to `Eventually`,
we prefer using functions which take a `Gomega` object as a parameter, which allows assertions to be made _inside_ that
function, leading to more detailed output in case of failures. Using a function that returns a boolean and expecting it
to succeed will simply lead to output about `false` expected to be `true`, which is harder to troubleshoot.

- Avoiding dependencies on ephemeral information, such as logs or events (and especially their absence). If that is
really necessary, deployments should be scaled down and back up before test runs.

## Dev Scripts & Running E2E Tests

Development scripts are provided under `/dev` to make it easier setting up a local development Fleet standalone
environment and running the E2E tests against it. These scripts are intended only for local Fleet development, not for
production nor any other real world scenario.

Setting up the local development environment and running the E2E tests is described in the [/dev/README.md](/dev/README.md).
