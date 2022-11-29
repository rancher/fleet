# Examining Performance Issues

Fleet differs from Rancher in one major design philosophy: nearly all "business logic" happens in the local cluster rather than in downstream clusters via agents.
The good news here is that the `fleet-controller` will tell us nearly all that we need to know via pod logs, network traffic and resource usage.
That being said, downstream `fleet-agent` deployments can perform Kubernetes API requests _back_ to the local cluster, which means that we have to monitor traffic inbound to the local cluster from our agents _as well as_ the outbound traffic we'd come to expect from the local `fleet-controller`.

While network traffic is major point of consideration, we also have to consider whether our performance issues are **compute-based**, **memory-based**, or **network-based**.
For example: you may encounter a pod with high compute usage, but that could be caused by heightened network traffic received from the _truly_ malfunctioning pod.

## Using pprof

[http pprof](https://pkg.go.dev/net/http/pprof) handlers are enabled by default with all [default profiles](https://pkg.go.dev/runtime/pprof#Profile) under the `/debug/pprof` prefix.

Additionally, it is possible to enable continuous CPU profiling for `fleetcontroller` to observe how CPU usage changes over time.

Add the following extra Helm values:
```yaml
cpuPprof:
  period: "60s"
  volumeConfiguration:
    hostPath:
      path: /tmp/pprof
      type: DirectoryOrCreate
```

Notes:
 - `period` is the pprof CPU period and can be changed with any other value
 - `volumeConfiguration` can be any valid volume configuration (not necessarily `hostPath`)

Alternatively, use the following Helm commandline arguments:
```
--set cpuPprof.period=60s \
--set cpuPprof.volumeConfiguration.hostPath.path=/tmp/pprof \
--set cpuPprof.volumeConfiguration.hostPath.type=DirectoryOrCreate \
```

If using `hostPath`, make sure that the target directory (`/tmp/pprof` in above examples) is writable.

Profiles can be inspected with the [pprof tool](https://github.com/google/pprof), eg.:

```sh
pprof -http=localhost:5000 ./2022-11-04_19_47_18.pprof.fleetcontroller.samples.cpu.pb.gz
```

