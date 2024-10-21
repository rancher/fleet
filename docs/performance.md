# Examining Performance Issues

Fleet differs from Rancher in one major design philosophy: nearly all "business logic" happens in the local cluster rather than in downstream clusters via agents.
The good news here is that the `fleet-controller` will tell us nearly all that we need to know via pod logs, network traffic and resource usage.
That being said, downstream `fleet-agent` deployments can perform Kubernetes API requests _back_ to the local cluster, which means that we have to monitor traffic inbound to the local cluster from our agents _as well as_ the outbound traffic we'd come to expect from the local `fleet-controller`.

While network traffic is major point of consideration, we also have to consider whether our performance issues are **compute-based**, **memory-based**, or **network-based**.
For example: you may encounter a pod with high compute usage, but that could be caused by heightened network traffic received from the _truly_ malfunctioning pod.

## Using pprof

[http pprof](https://pkg.go.dev/net/http/pprof) handlers are enabled by default with all [default profiles](https://pkg.go.dev/runtime/pprof#Profile) under the `/debug/pprof` prefix.

To collect profiling information continuously one can use https://github.com/rancherlabs/support-tools/tree/master/collection/rancher/v2.x/profile-collector
