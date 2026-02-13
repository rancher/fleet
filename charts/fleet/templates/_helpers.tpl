{{- define "system_default_registry" -}}
{{- if .Values.global.cattle.systemDefaultRegistry -}}
{{- printf "%s/" .Values.global.cattle.systemDefaultRegistry -}}
{{- else -}}
{{- "" -}}
{{- end -}}
{{- end -}}

{{/*
Windows cluster will add default taint for linux nodes,
add below linux tolerations to workloads could be scheduled to those linux nodes
*/}}
{{- define "linux-node-tolerations" -}}
- key: "cattle.io/os"
  value: "linux"
  effect: "NoSchedule"
  operator: "Equal"
{{- end -}}

{{- define "linux-node-selector" -}}
kubernetes.io/os: linux
{{- end -}}

{{/*
Resolve resources for a specific container.
Usage: {{ include "fleet.container-resources" (dict "root" $ "containerName" "fleetController") }}

Logic:
- If resources.<containerName> exists and is not empty -> use it
- If resources.<containerName> exists and is empty {} -> return nothing (explicit opt-out)
- If resources.<containerName> doesn't exist -> use default (resources.limits/requests) if available
- If no default exists -> return nothing
*/}}
{{- define "fleet.container-resources" -}}
{{- $root := .root -}}
{{- $containerName := .containerName -}}
{{- $resources := $root.Values.resources -}}
{{- if $resources -}}
  {{- if hasKey $resources $containerName -}}
    {{- $containerResources := index $resources $containerName -}}
    {{- if $containerResources -}}
resources:
  {{- toYaml $containerResources | nindent 2 }}
    {{- end -}}
  {{- else -}}
    {{- $hasDefault := or (hasKey $resources "limits") (hasKey $resources "requests") -}}
    {{- if $hasDefault -}}
      {{- $defaultResources := dict -}}
      {{- if hasKey $resources "limits" -}}
        {{- $_ := set $defaultResources "limits" $resources.limits -}}
      {{- end -}}
      {{- if hasKey $resources "requests" -}}
        {{- $_ := set $defaultResources "requests" $resources.requests -}}
      {{- end -}}
      {{- if $defaultResources -}}
resources:
  {{- toYaml $defaultResources | nindent 2 }}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}
