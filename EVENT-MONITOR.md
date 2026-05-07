# Fleet Event Monitor

`fleet-event-monitor` is a separate binary, Docker image, and Helm chart containing **read-only monitoring controllers**. These controllers:

- Mirror the exact watch configuration of Fleet's production controllers (same `SetupWithManager` logic)
- Log detailed diffs (spec, status, annotations, labels) when controllers are triggered
- Perform **no reconciliation or write operations**
- Are enabled/disabled per controller via environment variables or Helm values
- Use read-only RBAC permissions (only `get`, `list`, `watch` — except leases for leader election)

**Problem solved**: Understanding why Fleet controllers are triggered repeatedly or what specific changes cause reconciliation loops, without impacting production workloads.

---

## Codebase Layout

```
fleet/
├── cmd/fleeteventmonitor/main.go                          # Entry point
├── internal/cmd/monitor/
│   ├── root.go                                       # CLI / cobra setup, env var parsing
│   ├── operator.go                                   # controller-runtime manager, reconciler wiring
│   └── reconciler/
│       ├── monitor.go                                # Shared logging utilities (logSpecChange, logStatusChange, etc.)
│       ├── stats.go                                  # EventType constants, StatsTracker, Summary (JSON)
│       ├── filter.go                                 # EventTypeFilters struct, ResourceFilter, ShouldLog/ShouldLogTrigger logic
│       ├── cache.go                                  # ObjectCache (thread-safe, namespace/name keyed)
│       ├── predicate.go                              # TypedResourceVersionUnchangedPredicate
│       ├── bundle_monitor.go                         # Bundle controller (watches BD + Cluster)
│       ├── bundle_query.go                           # BundleQuery interface + impl (cluster→bundle mapping)
│       ├── cluster_monitor.go                        # Cluster controller (watches BD)
│       ├── bundledeployment_monitor.go               # BundleDeployment controller
│       ├── gitrepo_monitor.go                        # GitRepo controller (watches Job)
│       └── helmop_monitor.go                         # HelmOp controller
├── package/Dockerfile.event-monitor                  # Multi-arch Docker image (BCI 15.7, non-root)
├── charts/fleet-event-monitor/
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── _helpers.tpl
│       ├── deployment.yaml
│       └── rbac.yaml
└── .goreleaser.yaml                                  # Build/release config (fleet-event-monitor added)
```

**Original production controllers (for watch pattern reference)**:
- `internal/cmd/controller/reconciler/bundle_controller.go`
- `internal/cmd/controller/reconciler/cluster_controller.go`
- `internal/cmd/controller/reconciler/bundledeployment_controller.go`
- `internal/cmd/controller/gitops/reconciler/gitjob_controller.go`
- `internal/cmd/controller/helmops/reconciler/helmapp_controller.go`

---

## Architecture & Design Principles

### Design Pattern: Reuse Watch Logic, Replace Reconcile Logic

Each monitor controller:
1. Copies `SetupWithManager()` from the original controller (identical watches, predicates, event filters)
2. Replaces `Reconcile()` with logging-only logic
3. Uses an in-memory `ObjectCache` to detect changes between events

### Change Detection Flow

1. Reconcile triggered by watch event
2. `Get()` current object from Kubernetes API
3. Look up previous version from `ObjectCache`
4. If first time → log "create", cache object, return
5. If seen before → compare and log: spec diff, status diff, annotation/label/resourceVersion changes
6. Update cache with new version

### Watch Configurations (per controller)

| Controller | Primary Watch | Secondary Watches |
|---|---|---|
| Bundle | Bundle | BundleDeployment (status changes), Cluster (all changes) |
| Cluster | Cluster | BundleDeployment (spec/status changes) |
| BundleDeployment | BundleDeployment | — |
| GitRepo | GitRepo | Job (status changes) |
| HelmOp | HelmOp | — |

---

## Logging System

### Two Modes per Controller

Each controller independently operates in one of two modes, controlled by a per-controller `detailed` flag:

| Mode | `detailed` value | Behavior |
|---|---|---|
| **Summary** (default) | `false` | Counts events; prints periodic JSON summaries. No per-event log lines. |
| **Detailed** | `true` | Emits a structured log line for every event with diffs included. |

The summary printer **always runs** regardless of mode, so you always get aggregate statistics. Setting `detailed=true` adds verbose per-event logs on top.

### Per-Controller Detailed Logging

**Default**: all controllers default to `false` (summary only).

| Environment Variable | Helm Value | Default |
|---|---|---|
| `FLEET_EVENT_MONITOR_BUNDLE_DETAILED` | `logging.bundle.detailed` | `false` |
| `FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_DETAILED` | `logging.bundleDeployment.detailed` | `false` |
| `FLEET_EVENT_MONITOR_CLUSTER_DETAILED` | `logging.cluster.detailed` | `false` |
| `FLEET_EVENT_MONITOR_GITREPO_DETAILED` | `logging.gitRepo.detailed` | `false` |
| `FLEET_EVENT_MONITOR_HELMOP_DETAILED` | `logging.helmOp.detailed` | `false` |

> **Note**: The wrangler command framework does not parse boolean env vars automatically. They are manually parsed in `root.go` using `strconv.ParseBool()`. Valid values: `true`/`false`, `1`/`0`, `True`/`False`, `TRUE`/`FALSE`.

### Summary Configuration

| Environment Variable | Helm Value | Default | Description |
|---|---|---|---|
| `FLEET_EVENT_MONITOR_SUMMARY_INTERVAL` | `logging.summary.interval` | `"30s"` | How often to print the JSON summary |
| `FLEET_EVENT_MONITOR_SUMMARY_RESET` | `logging.summary.resetOnPrint` | `false` | Reset counters after each print (false = cumulative) |

### Event Types Tracked

| Event Type | Env var suffix / Helm key | Description |
|---|---|---|
| `generation-change` | `GENERATION_CHANGE` / `generationChange` | Spec modifications (generation bump) |
| `status-change` | `STATUS_CHANGE` / `statusChange` | Status field updates |
| `annotation-change` | `ANNOTATION_CHANGE` / `annotationChange` | Annotation modifications |
| `label-change` | `LABEL_CHANGE` / `labelChange` | Label modifications |
| `resourceversion-change` | `RESVER_CHANGE` / `resourceVersionChange` | Cache sync / metadata updates (finalizers, ownerRefs, managedFields) |
| `triggered-by` | `TRIGGERED_BY` / `triggeredBy` | Trigger source breakdown by resource type |
| `deletion` | `DELETION` / `deletion` | Resource being deleted |
| `not-found` | `NOT_FOUND` / `notFound` | Resource not found (likely deleted) |
| `create` | `CREATE` / `create` | First observation of resource |

### Summary Output Format (JSON)

```json
{
  "timestamp": "2026-02-09T10:00:30Z",
  "interval_seconds": 30,
  "summary": {
    "Bundle": {
      "fleet-local/test-bundle": {
        "generation-change": 5,
        "status-change": 20,
        "triggered-by": { "BundleDeployment": 12, "Cluster": 3 },
        "total_events": 41
      }
    }
  },
  "totals": { "total_resources_monitored": 3, "total_events": 63 }
}
```

### Useful Log Queries

```bash
# Find high-churn resources
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | \
  grep "Fleet Monitor Summary" | tail -1 | \
  jq -r '.summary.Bundle | to_entries[] | select(.value.total_events > 50) | "\(.key): \(.value.total_events) events"'

# Analyze trigger sources for a bundle
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | \
  grep "Fleet Monitor Summary" | tail -1 | \
  jq '.summary.Bundle["fleet-local/test-bundle"]["triggered-by"]'

# Get all status-change counts across all resources
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | \
  grep "Fleet Monitor Summary" | tail -1 | \
  jq -r '.summary | to_entries[] | .key as $t | .value | to_entries[] | select(.value["status-change"] > 0) | "\($t)/\(.key): \(.value["status-change"]) status changes"'

# Verify env vars are parsed correctly at startup
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | grep "parsed per-controller"

# Verify which controllers registered and in which mode
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | grep "registered monitor controller"
```

---

## Event Type Filtering

When a controller is in detailed mode (`detailed=true`), event type filters let you restrict which event types produce a log line. **Statistics are always tracked** regardless of filters — filters only affect the verbosity of the per-event log output.

**Default behavior**: if all event filter flags are `false`, **all event types are logged** (backwards compatible). To restrict output, set the specific types you want to `true`. Any `true` flag activates selective filtering.

### How It Works

`EventTypeFilters.IsEmpty()` returns true when all fields are false → `ShouldLog()` returns true for every event type. Once any field is set to `true`, only enabled types pass through.

### Env Var Reference (per controller)

The env var pattern is:
- Bundle: `FLEET_EVENT_MONITOR_BUNDLE_EVENT_<TYPE>`
- BundleDeployment: `FLEET_EVENT_MONITOR_BD_EVENT_<TYPE>`
- Cluster: `FLEET_EVENT_MONITOR_CLUSTER_EVENT_<TYPE>`
- GitRepo: `FLEET_EVENT_MONITOR_GITREPO_EVENT_<TYPE>`
- HelmOp: `FLEET_EVENT_MONITOR_HELMOP_EVENT_<TYPE>`

Where `<TYPE>` is one of: `GENERATION_CHANGE`, `STATUS_CHANGE`, `ANNOTATION_CHANGE`, `LABEL_CHANGE`, `RESVER_CHANGE`, `DELETION`, `NOT_FOUND`, `CREATE`, `TRIGGERED_BY`.

### Helm Values Reference

```yaml
logging:
  bundle:
    detailed: true   # Must be true for event filters to have any effect
    eventFilters:
      generationChange: false      # Set true to see spec diffs
      statusChange: false          # Set true to see status diffs
      annotationChange: false
      labelChange: false
      resourceVersionChange: false # Set true to see cache-sync/metadata events
      deletion: false
      notFound: false
      create: false
      triggeredBy: false           # Set true to see which resource triggered reconciliation
```

The same structure applies for `bundleDeployment`, `cluster`, `gitRepo`, and `helmOp`.

### Usage Examples

**Example 1: Only watch generation changes (spec diffs) for Bundle**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true \
  --set logging.bundle.eventFilters.generationChange=true
# All other event types are counted in summary but not logged
```

**Example 2: Focus on reconciliation trigger sources only**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true \
  --set logging.bundle.eventFilters.triggeredBy=true
# Shows which BD or Cluster changes are causing bundle reconciliations
```

**Example 3: See everything for Bundle (all filters false = log all)**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true
# All eventFilters default to false → all events are logged
```

**Example 4: Via environment variables**
```bash
FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true \
FLEET_EVENT_MONITOR_BUNDLE_EVENT_GENERATION_CHANGE=true \
FLEET_EVENT_MONITOR_BUNDLE_EVENT_TRIGGERED_BY=true \
./fleeteventmonitor --kubeconfig ~/.kube/config
```

**Example 5: Debug only cache-sync/metadata noise**
```bash
# See what managedFields/finalizer changes are causing resourceversion-only events
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.cluster.detailed=true \
  --set logging.cluster.eventFilters.resourceVersionChange=true
```

---

## Resource Filtering

Resource filters allow you to restrict monitoring to a specific subset of resources by namespace and/or name. This is useful in large deployments (100+ bundles) where you only care about specific resources and want to reduce log volume.

**Filters apply to both detailed logs AND statistics** — filtered-out resources do not appear in the JSON summary either.

### How It Works

At the top of each controller's `Reconcile()`, the resource namespace and name are tested against the compiled regex patterns. Resources that do not match are skipped entirely — no logs, no statistics.

- Both patterns are **regular expressions** (Go `regexp` syntax)
- An **empty pattern matches all** values for that field (backwards compatible)
- Patterns are compiled at startup; an **invalid regex causes the binary to exit** with a clear error message
- Namespace and name patterns are ANDed — a resource must match both to be monitored
- Filters are orthogonal to event type filtering — both can be combined

### Env Var Reference (per controller)

| Controller | Namespace Pattern | Name Pattern |
|---|---|---|
| Bundle | `FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAMESPACE` | `FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAME` |
| BundleDeployment | `FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_RESOURCE_FILTER_NAMESPACE` | `FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_RESOURCE_FILTER_NAME` |
| Cluster | `FLEET_EVENT_MONITOR_CLUSTER_RESOURCE_FILTER_NAMESPACE` | `FLEET_EVENT_MONITOR_CLUSTER_RESOURCE_FILTER_NAME` |
| GitRepo | `FLEET_EVENT_MONITOR_GITREPO_RESOURCE_FILTER_NAMESPACE` | `FLEET_EVENT_MONITOR_GITREPO_RESOURCE_FILTER_NAME` |
| HelmOp | `FLEET_EVENT_MONITOR_HELMOP_RESOURCE_FILTER_NAMESPACE` | `FLEET_EVENT_MONITOR_HELMOP_RESOURCE_FILTER_NAME` |

### Helm Values Reference

```yaml
logging:
  bundle:
    resourceFilter:
      namespace: ""  # Regular expression for namespace matching (e.g., "^fleet-local$")
      name: ""       # Regular expression for name matching (e.g., "^test-.*")
```

The same structure applies for `bundleDeployment`, `cluster`, `gitRepo`, and `helmOp`.

### Usage Examples

**Example 1: Monitor only a specific bundle**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true \
  --set "logging.bundle.resourceFilter.namespace=^fleet-local$" \
  --set "logging.bundle.resourceFilter.name=^my-app$"
```

**Example 2: Monitor all bundles in a namespace**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true \
  --set "logging.bundle.resourceFilter.namespace=^fleet-local$"
```

**Example 3: Monitor bundles matching a name prefix**
```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --set logging.bundle.detailed=true \
  --set "logging.bundle.resourceFilter.name=^payment-.*"
```

**Example 4: Via environment variables**
```bash
FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true \
FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAMESPACE="^fleet-local$" \
FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAME="^my-app$" \
./fleeteventmonitor --kubeconfig ~/.kube/config
```

**Example 5: Combine resource filter with event type filter**
```bash
# Monitor only status changes for a specific bundle in fleet-local
FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true \
FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAMESPACE="^fleet-local$" \
FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAME="^my-app$" \
FLEET_EVENT_MONITOR_BUNDLE_EVENT_STATUS_CHANGE=true \
./fleeteventmonitor --kubeconfig ~/.kube/config
```

---

## BundleQuery (Cluster → Bundle Mapping)

The Bundle monitor's Cluster watch handler queries which bundles are affected by a cluster change, logging the correct bundle name and namespace in trigger events.

`internal/cmd/monitor/reconciler/bundle_query.go` — adapted from `internal/cmd/controller/target/`:

```go
type BundleQuery interface {
    BundlesForCluster(context.Context, *fleet.Cluster) ([]*fleet.Bundle, []*fleet.Bundle, error)
}
```

Supports: basic targeting, label-based cluster matching, ClusterGroups, BundleNamespaceMapping (cross-namespace), Fleet agent bundles, deduplicated results.

**Without the query**: `Bundle reconciliation triggered Bundle= Namespace= Name= TriggeredBy=Cluster:my-cluster:fleet-default`

**With the query**: `Bundle reconciliation triggered Bundle=fleet-default/my-app Namespace=fleet-default Name=my-app TriggeredBy=Cluster:my-cluster:fleet-default`

---

## Configuration Reference (Helm Values)

Full `values.yaml` structure as shipped:

```yaml
image:
  repository: rancher/fleet-event-monitor
  tag: dev
  imagePullPolicy: IfNotPresent

namespace: cattle-fleet-system

# Enable/disable individual controllers
controllers:
  bundle: false
  bundledeployment: false
  cluster: false
  gitrepo: false
  helmop: false

# Worker counts per controller
workers:
  bundle: 5
  bundledeployment: 5
  cluster: 5
  gitrepo: 5
  helmop: 5

# Logging level / format
logFormat: json
logLevel: info
debug: false
debugLevel: 0

# Sharding (same as fleet controller)
shardID: ""

# Node selector and tolerations
nodeSelector: {}
tolerations: []
priorityClassName: ""

# Leader election
leaderElection:
  enabled: true
  leaseDuration: 30s
  retryPeriod: 10s
  renewDeadline: 25s

# Resource limits
resources:
  limits:
    cpu: 500m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

# Security context
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000

# Extra env vars injected verbatim
extraEnv: []

# Per-controller logging configuration
logging:
  bundle:
    detailed: false
    resourceFilter:
      namespace: ""
      name: ""
    eventFilters:
      generationChange: false
      statusChange: false
      annotationChange: false
      labelChange: false
      resourceVersionChange: false
      deletion: false
      notFound: false
      create: false
      triggeredBy: false

  bundleDeployment:
    detailed: false
    resourceFilter:
      namespace: ""
      name: ""
    eventFilters:
      generationChange: false
      statusChange: false
      annotationChange: false
      labelChange: false
      resourceVersionChange: false
      deletion: false
      notFound: false
      create: false
      triggeredBy: false

  cluster:
    detailed: false
    resourceFilter:
      namespace: ""
      name: ""
    eventFilters:
      generationChange: false
      statusChange: false
      annotationChange: false
      labelChange: false
      resourceVersionChange: false
      deletion: false
      notFound: false
      create: false
      triggeredBy: false

  gitRepo:
    detailed: false
    resourceFilter:
      namespace: ""
      name: ""
    eventFilters:
      # all false

  helmOp:
    detailed: false
    resourceFilter:
      namespace: ""
      name: ""
    eventFilters:
      # all false

  summary:
    interval: "30s"
    resetOnPrint: false
```

---

## Quick Start

### Build

```bash
go build -o bin/fleeteventmonitor ./cmd/fleeteventmonitor
```

### Run locally (standalone)

Set environment variables before running. At minimum, enable at least one controller:

```bash
# Enable the bundle controller in summary mode
export ENABLE_BUNDLE_EVENT_MONITOR=true
export NAMESPACE=cattle-fleet-system

./bin/fleeteventmonitor --kubeconfig ~/.kube/config
```

For detailed logging with filters:

```bash
export ENABLE_BUNDLE_EVENT_MONITOR=true
export NAMESPACE=cattle-fleet-system
export FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true
export FLEET_EVENT_MONITOR_BUNDLE_EVENT_STATUS_CHANGE=true
export FLEET_EVENT_MONITOR_BUNDLE_EVENT_TRIGGERED_BY=true

./bin/fleeteventmonitor --kubeconfig ~/.kube/config
```

To narrow down to a specific resource:

```bash
export ENABLE_BUNDLE_EVENT_MONITOR=true
export NAMESPACE=cattle-fleet-system
export FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true
export FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAMESPACE="^fleet-local$"
export FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAME="^my-app$"

./bin/fleeteventmonitor --kubeconfig ~/.kube/config
```

### Deploy with Helm

```bash
# All controllers in summary mode
helm install fleet-event-monitor ./charts/fleet-event-monitor \
  --namespace cattle-fleet-system \
  --set controllers.bundle=true \
  --set controllers.bundledeployment=true \
  --set controllers.cluster=true \
  --set controllers.gitrepo=true \
  --set controllers.helmop=true

# Bundle controller in detailed mode, all event types
helm install fleet-event-monitor ./charts/fleet-event-monitor \
  --namespace cattle-fleet-system \
  --set controllers.bundle=true \
  --set logging.bundle.detailed=true

# Only log generation-change and triggered-by events for Bundle
helm install fleet-event-monitor ./charts/fleet-event-monitor \
  --namespace cattle-fleet-system \
  --set controllers.bundle=true \
  --set logging.bundle.detailed=true \
  --set logging.bundle.eventFilters.generationChange=true \
  --set logging.bundle.eventFilters.triggeredBy=true
```

### Upgrade and change config without rebuilding

```bash
helm upgrade fleet-event-monitor ./charts/fleet-event-monitor \
  --reuse-values \
  --set logging.cluster.detailed=true \
  --set logging.cluster.eventFilters.statusChange=true
```

### With sharding

```bash
helm install fleet-event-monitor-shard0 ./charts/fleet-event-monitor --set shardID=shard0
helm install fleet-event-monitor-shard1 ./charts/fleet-event-monitor --set shardID=shard1
```

---

## RBAC

ClusterRole: `get`, `list`, `watch` on Fleet resources, core resources, RBAC resources, Jobs, Deployments.
Role (namespaced): `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` on `coordination.k8s.io/leases` (leader election only).
No write access to any Fleet or Kubernetes resources.

---

## Known Limitations

| Limitation | Workaround |
|---|---|
| Controller-runtime doesn't expose which watch triggered a reconciliation | Log at fan-out mapping functions (Cluster→Bundle handler, BD→Bundle handler) |
| `TypedResourceVersionUnchangedPredicate` causes cache-sync noise | Filter using `eventFilters.resourceVersionChange=false` to suppress in detailed mode |

---

## Dev Scripts

Several scripts in `dev/` help parse and visualize monitor output. They all read from stdin, a pipe, or a file argument and require `jq`.

### `dev/format-monitor-summary.sh` — pretty-print the JSON summary

Parses all `Fleet Monitor Summary` lines from a log stream and renders the last (or cumulative) summary as a human-readable table. Also computes the time range covered if multiple summaries are present.

```bash
# From a running pod
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | ./dev/format-monitor-summary.sh

# From a saved log file
./dev/format-monitor-summary.sh logs.json
```

Output example:
```
================================================================================
  FLEET MONITOR SUMMARY
================================================================================
  Timestamp:        2026-02-09T10:00:30Z
  Interval:         30s
  Total Resources:  3
  Total Events:     63
================================================================================

▼ Bundle
-------------------------------------------------------------------------------
  RESOURCE               CREATE   DELETE  N-FOUND   STATUS  GEN-CHG    ANNOT    LABEL   RESVER   EVENTS
  ---------------------- ------ -------- ------- -------- ------- ----- ----- ------ ------
  fleet-local/my-app          1        0       0       20       5       0       0       0       41
    └─ triggered-by: BundleDeployment = 12
    └─ triggered-by: Cluster = 3
================================================================================
  Time range: ...
================================================================================
```

### `dev/parse-status-log.sh` — visualize status change diffs

Filters `status-change` events from detailed log output and renders each diff with colour-coded `+`/`-` lines.

Requires the controller to be running in detailed mode with `statusChange` enabled:
```bash
export FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true
export FLEET_EVENT_MONITOR_BUNDLE_EVENT_STATUS_CHANGE=true
```

Usage:
```bash
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | ./dev/parse-status-log.sh
```

### `dev/parse-resourceversion-log.sh` — visualize resource version change diffs

Filters `resourceversion-change` events and renders each event with version numbers, change reason, metadata change list, and colour-coded diff output. Useful for identifying which SSA managers or finalizer changes are causing metadata-only reconciliation loops.

Requires the controller to be running in detailed mode with `resourceVersionChange` enabled:
```bash
export FLEET_EVENT_MONITOR_BUNDLE_DETAILED=true
export FLEET_EVENT_MONITOR_BUNDLE_EVENT_RESVER_CHANGE=true
```

Usage:
```bash
kubectl logs -n cattle-fleet-system deploy/fleet-event-monitor | ./dev/parse-resourceversion-log.sh
```

The output includes a `Changed:` line listing which metadata fields changed (`finalizers`, `ownerReferences`, `managedFields`) and, for `managedFields`, a manager-level summary (added/removed/changed SSA managers) followed by a field-level diff of their `FieldsV1` entries.
