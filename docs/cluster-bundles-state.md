# Cluster and Bundle state

Clusters and Bundles have different states in each phase of applying Bundles.

## Bundles

**Ready**: Bundles have been deployed and all resources are ready.

**NotReady**: Bundles have been deployed and some resources are not ready.

**WaitApplied**: Bundles have been synced from Fleet controller and downstream cluster, but are waiting to be deployed.

**ErrApplied**: Bundles have been synced from the Fleet controller and the downstream cluster, but there were some errors when deploying the Bundle.

**OutOfSync**: Bundles have been synced from Fleet controller, but downstream agent hasn't synced the change yet.

**Pending**: Bundles are being processed by Fleet controller.

**Modified**: Bundles have been deployed and all resources are ready, but there are some changes that were not made from the Git Repository.

## Clusters

**WaitCheckIn**: Waiting for agent to report registration information and cluster status back.

**NotReady**: There are bundles in this cluster that are in NotReady state. 

**WaitApplied**: There are bundles in this cluster that are in WaitApplied state.

**ErrApplied**: There are bundles in this cluster that are in ErrApplied state.

**OutOfSync**: There are bundles in this cluster that are in OutOfSync state.

**Pending**: There are bundles in this cluster that are in Pending state.

**Modified**: There are bundles in this cluster that are in Modified state.

**Ready**: Bundles in this cluster have been deployed and all resources are ready.
