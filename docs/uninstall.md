# Uninstall

Fleet is packaged as two Helm charts so uninstall is accomplished by
uninstalling the appropriate Helm charts. To uninstall Fleet run the following
two commands:

```shell
helm -n cattle-fleet-system uninstall fleet
helm -n cattle-fleet-system uninstall fleet-crd
```