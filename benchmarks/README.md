# Fleet Benchmark Suite

The **Fleet benchmark suite** is a comprehensive performance testing framework designed to measure the performance of Fleet's GitOps controllers in real-world scenarios. It runs against an existing Fleet installation and collects extensive metrics about controller behavior and system performance.

## What the Benchmarks Measure

The benchmark suite measures **core experiments** that align with Fleet's bundle lifecycle:

### 1. GitOps Performance - GitRepo to Bundle Creation

- **`create-1-gitrepo-50-bundle`**: Measures how fast Fleet can create 50 bundles from a single GitRepo
- **`create-50-gitrepo-50-bundle`**: Tests scaling with 50 GitRepos creating 50 bundles total

### 2. Targeting Performance - Bundle to BundleDeployment

- **`create-50-bundle`**: Creates 50 bundles and measures how long it takes to target them to clusters (creating BundleDeployments)
- **`create-150-bundle`**: Creates 150 bundles targeting all clusters (skips if >1000 clusters to avoid overwhelming etcd)

### 3. Deployment Performance - BundleDeployment to Ready Resources

- **`create-1-bundledeployment-10-resources`**: Measures deployment of 1 BundleDeployment resulting in 10 Kubernetes resources per cluster
- **`create-50-bundledeployment-500-resources`**: Tests larger-scale deployment with 50 BundleDeployments creating 500 resources per cluster

## Comprehensive Metrics Collected

For each experiment, the suite collects:

### 1. Duration Metrics

- Total experiment duration (time from resource creation to ready state)
- Individual reconciliation times
- Queue processing durations

### 2. Resource Metrics

- Total Kubernetes resource counts (before/during/after)
- Fleet-specific resources (Clusters, Bundles, BundleDeployments, GitRepos, Contents)
- Standard K8s resources (Pods, Services, ConfigMaps, Secrets, Deployments, etc.)

### 3. Memory Metrics

- Memory usage before, during, and after each experiment
- Tracks controller memory consumption

### 4. Controller Metrics (if enabled)

- Reconcile success/error/requeue counts
- CPU usage
- Network RX/TX bytes
- REST client request counts
- Workqueue operations (adds, retries, durations)
- Garbage collection duration

### 5. Environment Context

- Number of clusters in the workspace
- Number of nodes and their capacity (CPU, Memory, Pods)
- CRD count
- Kubernetes version
- Container images present

## Running the Benchmarks

### Prerequisites

- A running Fleet installation
- At least one cluster labeled with `fleet.cattle.io/benchmark=true`
- `kubectl` configured with access to the cluster

### Configuration via Command Line Arguments

- **`--namespace`** / **`-n`**: Cluster registration namespace (default: `fleet-local`)
- **`--timeout`** / **`-t`**: Timeout for experiments (default: `5m`)
- **`--db`** / **`-d`**: Path to JSON file database directory (default: `db/`)
- **`--verbose`** / **`-v`**: Enable verbose output (default: `false`)

Note: Metrics collection is always enabled. Output files are automatically generated with timestamp-based names (`b-<timestamp>.json`).

### Running the Benchmarks

```bash
# Released binary with bundled assets
fleet-benchmark run
# See -h for options
fleet-benchmark run -n fleet-default -t 1h

# Development mode
go run ./benchmarks/cmd run -n fleet-default
```

## Key Features

- **Realistic Testing**: Uses actual Fleet installations, not mocks
- **Multi-cluster Support**: Tests can run against configurations with thousands of clusters
- **Paused Bundle Strategy**: For deployment tests, bundles are created paused first to avoid missing early reconciliation events
- **Label-based Selection**: Uses `fleet.cattle.io/benchmark=true` label to identify clusters to include
- **Automated Reporting**: Outputs results in JSON, CSV formats with statistical analysis tools
- **Resource Cleanup**: Automatically cleans up test resources after each experiment

## Reporting Tools

The `cmd/` directory includes tools to analyze benchmark results:

### Available Commands

- **`run`**: Execute benchmarks against the current cluster
- **`report`**: Report on a Ginkgo JSON output file
- **`json`**: Print report as JSON
- **`csv`**: Print report setup as CSV
- **`completion`**: Generate shell autocompletion scripts
- **`help`**: Get help about any command

### Examples

```bash
# Run benchmarks
go run ./benchmarks/cmd run -n fleet-default

# View report from JSON output
go run ./benchmarks/cmd report -i b-1234567890.json

# Generate CSV export
go run ./benchmarks/cmd csv -i b-1234567890.json

# Print report as JSON
go run ./benchmarks/cmd json -i b-1234567890.json
```

## Database Comparison Feature

The benchmark suite supports comparing current benchmark results against a historical database of previous runs. This enables **performance regression detection** and **trend analysis** over time.

### How It Works

1. **Database Directory**: The `--db` flag (default: `db/`) specifies a directory containing historical benchmark JSON files
2. **Automatic Comparison**: When you run the `report` command, it loads all JSON files from the database directory and compares your current run against the historical population
3. **Statistical Analysis**: The tool calculates:
   - **Mean Duration**: Average duration across all historical runs
   - **Standard Deviation**: Variability in historical performance
   - **Z-Score**: How many standard deviations the current run differs from the mean (negative = faster/better, positive = slower/worse)
4. **Final Score**: An aggregate Z-score across all experiments provides an overall performance assessment

### Building Your Database

After each benchmark run, the tool suggests moving the report to the database folder:

```bash
# Run benchmarks (generates b-2024-01-15_10:30:45.json)
fleet-benchmark run

# Move the report to your database folder
mv b-2024-01-15_10:30:45.json db/

# Future runs will automatically compare against this baseline
fleet-benchmark run
```

### Comparison Modes

The report adapts based on available historical data:

- **No database** (0 reports): Shows only current run results
- **Single baseline** (1 report): Direct comparison showing "better" or "worse" 
- **Statistical analysis** (2+ reports): Full Z-score analysis with mean, standard deviation, and confidence levels

### Example Usage

```bash
# Run benchmarks with custom database location
fleet-benchmark run --db=/path/to/my-benchmarks/

# Generate report comparing against database
fleet-benchmark report --db=/path/to/my-benchmarks/ -i b-latest.json

# View detailed statistics including StdDev and Z-scores
fleet-benchmark report --db=db/ -i b-latest.json --stats
```

### Understanding the Output

When comparing against a database with 2+ samples:

```
# Summary for each Experiment

| Experiment                           | Duration  | Mean Duration | StdDev Duration | ZScore | Status  |
|--------------------------------------|-----------|---------------|-----------------|--------|---------|
| create-1-gitrepo-50-bundle          | 45.23s    | 47.50s        | 2.15s          | -1.06  | better  |
| create-50-bundle                     | 38.91s    | 35.20s        | 1.80s          | 2.06   | worse   |

# Final Score
b-2024-01-15_10:30:45.json: -0.45
```

- **Negative Z-Score**: Current run is faster than average (good)
- **Positive Z-Score**: Current run is slower than average (potential regression)
- **Final Score**: Average of all Z-scores (negative = overall improvement)

## How Benchmarks Use Ginkgo

### Unique Test Structure

The benchmark suite uses Ginkgo but with a **non-standard architecture** that differs from typical Go test suites:

1. **No `_test.go` suffix**: The test files (`suite.go`, `gitrepo_bundle.go`, `targeting.go`, `deploy.go`) intentionally don't use the `_test.go` naming convention. This is a **critical design decision** due to Go's build constraints:
   - Files with `_test.go` suffix are only compiled in test mode and are excluded from normal package builds
   - The CLI wrapper (`benchmarks/cmd`) needs to import the `benchmarks` package to call `benchmarks.TestBenchmarkSuite`
   - If the tests used `_test.go` suffix, they wouldn't be available for import, causing compilation errors
   - By avoiding the suffix, the Ginkgo tests become part of the regular package and can be invoked programmatically

2. **Embedded Assets**: The suite uses Go's `embed` package to bundle test resources at compile time:
   ```go
   //go:embed assets
   assets embed.FS
   ```
   This embeds the entire `assets/` directory structure into the compiled binary, allowing the benchmark binary to be self-contained and portable.

3. **Custom CLI Wrapper**: The `benchmarks/cmd/` directory contains a custom CLI application that:
   - Wraps the Ginkgo test execution using `testing.MainStart()`
   - Provides a user-friendly command-line interface via Cobra
   - Translates command-line arguments to environment variables (implementation detail)
   - Automatically generates and analyzes reports after test completion

### Running Tests: The Correct Way

**❌ Don't run with:**
```bash
go test ./benchmarks/...       # Won't work - no _test.go files
ginkgo run ./benchmarks/       # Won't work - needs custom setup
```

**✅ Instead, run with:**
```bash
# Using the compiled binary (recommended for releases)
fleet-benchmark run

# Or directly during development
go run ./benchmarks/cmd run -n fleet-default
```

### How It Works Under the Hood

The `benchmarks/cmd/run.go` file:

1. Validates the environment (checks for labeled clusters)
2. Parses command-line arguments and translates them to environment variables (implementation detail for communication with the test suite):
   ```go
   os.Setenv("FLEET_BENCH_OUTPUT", report)
   os.Setenv("FLEET_BENCH_TIMEOUT", timeout.String())
   os.Setenv("FLEET_BENCH_NAMESPACE", namespace)
   os.Setenv("FLEET_BENCH_METRICS", "true")
   ```
3. Programmatically invokes the Ginkgo suite using `testing.MainStart()`:
   ```go
   testing.MainStart(
       matchStringOnly(nil),
       []testing.InternalTest{
           {"BenchmarkSuite", benchmarks.TestBenchmarkSuite},
       },
       nil, nil, nil,
   ).Run()
   ```
4. Automatically runs the report command after tests complete

### Asset Management

The embedded assets are unpacked at runtime:
- `BeforeSuite` creates a temporary directory
- Assets are extracted from the embedded filesystem to disk
- The `kubectl` client uses real files from this temporary location
- `AfterSuite` cleans up the temporary directory

This approach allows:
- **Portability**: The binary contains all test resources
- **Version control**: Assets are versioned with the code
- **Reproducibility**: Same binary produces consistent results across environments

## Directory Structure

```
benchmarks/
├── assets/                    # Test resources (embedded in binary)
│   ├── create-1-gitrepo-50-bundle/
│   ├── create-50-gitrepo-50-bundle/
│   ├── create-bundle/
│   ├── create-1-bundledeployment-10-resources/
│   └── create-bundledeployment-500-resources/
├── cmd/                       # CLI wrapper and reporting tools (package main)
│   ├── run.go                # Executes the benchmark suite
│   ├── report.go             # Generates comparison reports
│   ├── json.go               # JSON report parsing
│   ├── csv.go                # CSV export
│   └── parser/               # Ginkgo report parsing utilities
├── record/                    # Metrics collection logic
├── report/                    # Report generation and formatting
├── suite.go                   # Ginkgo test suite setup (package benchmarks)
├── gitrepo_bundle.go         # GitOps performance tests
├── targeting.go              # Targeting performance tests
└── deploy.go                 # Deployment performance tests
```

## Use Cases

This benchmark suite is essential for:

- **Performance Regression Detection**: Identify performance degradation between Fleet versions
- **Controller Optimization**: Measure impact of controller code changes
- **Scalability Validation**: Verify Fleet can handle large numbers of clusters and deployments
- **Capacity Planning**: Understand resource requirements for different deployment scales
- **Historical Tracking**: Build a database of performance metrics over time

## Notes

- The benchmarks use a fixed random seed for reproducibility
- Tests fail fast on first error for quick feedback
- Memory metrics track the test process, not the controller (unless using metrics collection)
- Controller metrics use the Prometheus metrics endpoints
