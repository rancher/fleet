GitOps and CI/CD
================

Fleet is designed to be used in a CD or GitOpts pipeline. Because Fleet is a standard
Kubernetes API it should integrate well in the existing ecosystem.  One can use a
tool such as ArgoCD or Flux in the Fleet manager cluster to copy resources from Git to
the Fleet manager.
 
Often a more traditional CI approach is much easier than running ArgoCD or Flux.  For traditional CI
one just needs to run `flt test` and `flt apply` as a part of the CI process.  An example doing this with GitHub Actions
is below.

GitOps Patterns
===============

There are two scenarios to consider for GitOps.  First is managing the resources in the Fleet manager itself so that
it can then manage clusters.  The reason you do this as opposed to going directly to the clusters is that intention
of the Fleet manager is that as you add/delete clusters the clusters can immediately assume the configuration they are
supposed to. Also Fleet manager will roll out deployments in a way not easily possible with GitOps.

The second scenario to consider is using Fleet manager to define the GitOps pipelines that run in a cluster.  You can
use Fleet manager to define the pipelines and then once the pipeline is established it goes directly to the cluster not
through the Fleet manager.

GitHub Actions Example
======================

Coming soon.... (or is it?)