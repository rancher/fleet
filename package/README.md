 # Packages

 This directory contains all the files needed for packaging Fleet images.

 ## Testing Windows

Testing Windows images locally can be done using an SAC/LTSC host.
You can clone your Git repository and checkout your developer branch to get started.

First, enter `powershell` and install Git.
You can use [Chocolatey](https://chocolatey.org/install) to [install the package](https://chocolatey.org/packages/git).

Next, clone the repository and checkout your branch.

```powershell
& 'C:\Program Files\Git\bin\git.exe' clone https://github.com/<user>/<fleet-fork>.git
& 'C:\Program Files\Git\bin\git.exe' checkout --track origin/<dev-branch>
```

Finally, you can build the image with an uploaded binary from a tagged release.
This is useful when testing the `agent` in a Windows cluster.

```powershell
docker build -t test -f package\Dockerfile-windows.agent --build-arg SERVERCORE_VERSION=1909 --build-arg RELEASES=releases.rancher.com --build-arg VERSION=v0.3.4-rc4 .
```
