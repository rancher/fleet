# Removing

If you delete a `GitRepo` from the Fleet Manager the `Bundles` created by the git repo are not automatically removed.
This is to prevent accidentally deleting software from clusters by just modifying the git repos. To fully remove
the deployed software just delete the corresponding bundles too. This can be done by running

```shell
kubectl -n "${REPO_NAMESPACE}" delete bundles.fleet.cattle.io -l fleet.cattle.io/repo-name="${REPO_NAME}"
```