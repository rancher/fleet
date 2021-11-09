# Troubleshooting

This is a collection of information, which help you to troubleshoot if fleet is not working

## Check status of fleet controller

You can check the status of the Fleet controller pods by running the below commands.

```bash
kubectl -n fleet-system logs -l app=fleet-controller
kubectl -n fleet-system get pods -l app=fleet-controller
```

```bash
NAME                                READY   STATUS    RESTARTS   AGE
fleet-controller-64f49d756b-n57wq   1/1     Running   0          3m21s
```

## Fleet fails with bad response code: 403

If you see your GitJob complaining with this error

```
time="2021-11-04T09:21:24Z" level=fatal msg="bad response code: 403"
```

This may indicate that fleet cannot access the helm repo you specified in your [`fleet.yaml`](./gitrepo-structure.md).

- check if your repo is accessible from your dev machine and you can actually download the helm chart
- check if your credentials for the git repo are fine