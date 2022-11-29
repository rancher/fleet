# Windows Development

Fleet agents run natively on both Windows and Linux downstream Kubernetes nodes.
While Fleet is currently unsupported for _local_ Windows clusters, we need to test its usage of _downstream_ Windows clusters.

Once your Windows agent is built (following similar directions from Linux agent development above), start Rancher on a Kubernetes cluster and perform the following steps:

1. Create a downstream RKE1 or RKE2 Windows cluster
2. Wait for `fleet-agent` to be deployed downstream (native on Linux and Windows, but will likely default to Linux)
3. Change `fleet-agent` Deployment image name and tag to be your custom agent image name and tag
4. Change `fleet-agent` Deployment image `PullPolicy` to `Always`
5. Delete the existing `fleet-agent` pod and wait for the new one to reach a `Running` state _(note: ensure there aren't any non-transient error logs)_
6. Create the [multi-cluster/windows-helm](https://github.com/rancher/fleet-examples/tree/master/multi-cluster/windows-helm) GitRepo CR in the local cluster
7. Observe "Active" or "Running" or "Completed" states for `gitrepos.fleet.cattle.io` and other resources (e.g. pods deployed from the chart)

## Building `fleet-agent` for Windows

Testing Windows images locally can be done on a Windows host.

First, enter `powershell` and install Git.
You can use [Chocolatey](https://chocolatey.org/install) to [install the package](https://chocolatey.org/packages/git).

Next, clone the repository and checkout your branch.

```powershell
& 'C:\Program Files\Git\bin\git.exe' clone https://github.com/<user>/<fleet-fork>.git
& 'C:\Program Files\Git\bin\git.exe' checkout --track origin/<dev-branch>
```

Finally, you can build the image with an uploaded binary from a tagged release.
This is useful when testing the `fleet-agent` on a Windows cluster.

```powershell
docker build -t test -f package\Dockerfile-windows.agent --build-arg SERVERCORE_VERSION=<windows-version> --build-arg RELEASES=releases.rancher.com --build-arg VERSION=<fleet-tag> .
```

