#!/bin/bash

# Write a new definition of the `known-hosts` config map in the fleet chart based on updated entries for each provider.
# Entries are obtained through `ssh-keyscan` and sorted lexically to preserve ordering and hence prevent false
# positives.
providers=(
    "bitbucket.org"
    "github.com"
    "gitlab.com"
    "ssh.dev.azure.com"
    "vs-ssh.visualstudio.com"
)

dst=charts/fleet/templates/configmap_known_hosts.yaml
echo "apiVersion: v1" > "$dst"
echo "kind: ConfigMap" >> "$dst"
echo "metadata:" >> "$dst"
echo "  name: known-hosts" >> "$dst"
echo "data:" >> "$dst"
echo "  known_hosts: |" >> "$dst"

for prov in "${providers[@]}"; do
    ssh-keyscan "$prov" | grep "^$prov" | sort -b | sed 's/^/    /' >> "$dst"
done

echo '{{- if .Values.additionalKnownHosts }}
    {{ range .Values.additionalKnownHosts -}}
    {{ . }}
    {{ end -}}
{{- end }}' >> "$dst"