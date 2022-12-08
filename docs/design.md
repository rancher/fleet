# Design

More information can be found in the "Reference" section on the [fleet docs site](https://fleet.rancher.io/next/ref-components).

## Design for Fleet Managing Fleet: Hosted Rancher, Harvester, Rancher Managing Rancher, etc.

Starting with Fleet v0.3.7 and Rancher v2.6.1, scenarios where Fleet is managing Fleet (i.e. Rancher managing Rancher) will result in _two_ `fleet-agent` deployments running every _managed_ Fleet cluster.
The agents will be communicating with two different `fleet-controller` deployments.

```
 Local Fleet Cluster                  Managed Fleet Cluster                     Downstream Cluster
┌───────────────────────────────┐    ┌────────────────────────────────────┐    ┌────────────────────────────────────┐
│                               │    │                                    │    │                                    │
│ ┌────cattle-fleet-system────┐ │    │ ┌──────cattle-fleet-system───────┐ │    │                                    │
│ │                           │ │    │ │                                │ │    │                                    │
│ │  ┌─────────────────────┐  │ │    │ │  ┌──────────────────────────┐  │ │    │                                    │
│ │  │  fleet-controller   ◄──┼─┼────┼─┼──► fleet-agent (downstream) │  │ │    │                                    │
│ │  └─────────────────────┘  │ │    │ │  └──────────────────────────┘  │ │    │                                    │
│ │                           │ │    │ │                                │ │    │                                    │ 
│ └───────────────────────────┘ │    │ │                                │ │    │                                    │
│                               │    │ │                                │ │    │                                    │
│ ┌─cattle-fleet-local-system─┐ │    │ │                                │ │    │ ┌──────cattle-fleet-system───────┐ │
│ │                           │ │    │ │                                │ │    │ │                                │ │
│ │  ┌─────────────────────┐  │ │    │ │  ┌──────────────────────────┐  │ │    │ │  ┌──────────────────────────┐  │ │
│ │  │ fleet-agent (local) │  │ │    │ │  │    fleet-controller      ◄──┼─┼────┼─┼──► fleet-agent (downstream) │  │ │
│ │  └─────────────────────┘  │ │    │ │  └──────────────────────────┘  │ │    │ │  └──────────────────────────┘  │ │
│ │                           │ │    │ │                                │ │    │ │                                │ │
│ └───────────────────────────┘ │    │ └────────────────────────────────┘ │    │ └────────────────────────────────┘ │
│                               │    │                                    │    │                                    │
└───────────────────────────────┘    │ ┌───cattle-fleet-local-system────┐ │    └────────────────────────────────────┘
                                     │ │                                │ │
                                     │ │  ┌──────────────────────────┐  │ │
                                     │ │  │   fleet-agent (local)    │  │ │
                                     │ │  └──────────────────────────┘  │ │
                                     │ │                                │ │
                                     │ └────────────────────────────────┘ │
                                     │                                    │
                                     └────────────────────────────────────┘
```

## Design for Fleet in Rancher v2.6+

Fleet is a required component of Rancher as of Rancher v2.6+.
Fleet clusters are tied directly to native Rancher object types accordingly:

```
┌───────────────────────────────────┐  ==  ┌────────────────────────────────────┐  ==  ┌──────────────────────────────────┐
│ clusters.fleet.cattle.io/v1alpha1 ├──────┤ clusters.provisioning.cattle.io/v1 ├──────┤ clusters.management.cattle.io/v3 │
└────────────────┬──────────────────┘      └───────────────────┬────────────────┘      └──────────────────────────────────┘
                 │                                             │
                 └──────────────────────┬──────────────────────┘
                                        │
                          ┌─────────────▼────────────────────────┐
                          │                                      │
       ┌──────────────────▼──────────────────────┐  ==  ┌────────▼──────┐
       │ fleetworkspaces.management.cattle.io/v3 ├──────┤ namespaces/v1 │
       └─────────────────────────────────────────┘      └───────────────┘
```

---

## Attributions

- ASCII charts created with [asciiflow](https://asciiflow.com/)
