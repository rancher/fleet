# Fleet Helm Chart

Fleet is GitOps and HelmOps at scale. Fleet is designed to manage multiple clusters.

## What is Fleet?

* Cluster engine: Fleet is a container management and deployment engine designed to offer users more control on the local cluster and constant monitoring through GitOps. Fleet focuses not only on the ability to scale, but it also gives users a high degree of control and visibility to monitor exactly what is installed on the cluster.

* Deployment management: Fleet can manage deployments from git of raw Kubernetes YAML, Helm charts, Kustomize, or any combination of the three. Regardless of the source, all resources are dynamically turned into Helm charts, and Helm is used as the engine to deploy all resources in the cluster. As a result, users can enjoy a high degree of control, consistency, and auditability of their clusters.

## Introduction

This chart deploys Fleet on a Kubernetes cluster. It also deploys some of its dependencies as subcharts.

The documentation is centralized in the [doc website](https://fleet.rancher.io/).

## Prerequisites

Get helm if you don't have it. Helm 3 is just a CLI.


## Install Fleet

Install the Fleet Helm charts (there are two because we separate out CRDs for ultimate flexibility.):

```
$ helm repo add fleet https://rancher.github.io/fleet-helm-charts/
$ helm -n cattle-fleet-system install --create-namespace --wait fleet-crd fleet/fleet-crd
$ helm -n cattle-fleet-system install --create-namespace --wait fleet fleet/fleet
```