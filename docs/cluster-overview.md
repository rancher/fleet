# Overview

There are two specific styles to registering clusters. These styles will be referred
to as **agent initiated** and **manager initiated** registration. Typically one would
go with the agent initiated registration but there are specific use cases in which
manager initiated is a better workflow.

## Agent Initiated Registration
Agent initiated refers to a pattern in which the downstream cluster installs an agent with a 
[cluster registration token](./cluster-tokens.md) and optionally a client ID. The cluster
agent will then make a API request to the Fleet manager and initiate the registration process. Using
this process the Manager will never make an outbound API request to the downstream clusters and will thus
never need to have direct network access. The downstream cluster only needs to make outbound HTTPS
calls to the manager.

## Manager Initiated Registration

Manager initiated registration is a process in which you register an existing Kubernetes cluster
with the Fleet manager and the Fleet manager will make an API call to the downstream cluster to
deploy the agent. This style can place additional network access requirements because the Fleet
manager must be able to communicate with the downstream cluster API server for the registration process.
After the cluster is registered there is no further need for the manager to contact the downstream
cluster API.  This style is more compatible if you wish to manage the creation of all your Kubernetes
clusters through GitOps using something like [cluster-api](https://github.com/kubernetes-sigs/cluster-api)
or [Rancher](https://github.com/rancher/rancher).