package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	command "github.com/rancher/fleet/internal/cmd"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// NewBundleDiff returns a subcommand to display bundle diffs from status
func NewBundleDiff() *cobra.Command {
	cmd := command.Command(&BundleDiff{}, cobra.Command{
		Use:   "bundlediff [flags]",
		Short: "Display bundle diffs from resource status",
		Long: `Display bundle diffs from resource status.

This command extracts and displays the diff patches from Bundle or BundleDeployment
resources that have been modified. The diffs show the differences between the desired
state (from Git/Helm) and the actual state in the cluster.

For BundleDeployments, the command shows the patch information from the ModifiedStatus
field, which contains JSON patches indicating what has been changed on deployed resources.

For Bundles, the command aggregates diff information from all associated BundleDeployments
across target clusters.

By default, the command searches for BundleDeployments across all namespaces. You can
restrict to a specific namespace using the -n flag, which is useful when querying a
specific BundleDeployment by name.

The output format can be either human-readable text (default) or JSON.

Examples:
  # Show diffs for all Bundles (grouped by bundle) across all namespaces
  fleet bundlediff

  # Show all BundleDeployments for a specific Bundle
  fleet bundlediff --bundle my-bundle

  # Show a specific BundleDeployment in a cluster namespace
  fleet bundlediff --bundle-deployment my-bundle-deployment -n cluster-fleet-local-local-abc123

  # Output in JSON format
  fleet bundlediff --json

  # Output as fleet.yaml diff snippet for a specific BundleDeployment
  # This generates a diff section you can add to your fleet.yaml in Git
  fleet bundlediff --fleet-yaml --bundle-deployment my-bundle-deployment -n cluster-fleet-local-local-abc123

  # Show diffs only in a specific namespace
  fleet bundlediff -n cluster-fleet-local-local-abc123`,
	})
	cmd.SetOut(os.Stdout)

	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
	return cmd
}

type BundleDiff struct {
	FleetClient
	BundleDeployment string `usage:"Name of the BundleDeployment to show diffs for" short:""`
	Bundle           string `usage:"Name of the Bundle to show diffs for all its BundleDeployments" short:"b"`
	JSON             bool   `usage:"Output in JSON format"`
	FleetYAML        bool   `usage:"Output in fleet.yaml format (comparePatches)"`
}

type DiffOutput struct {
	BundleDeploymentName string                 `json:"bundleDeploymentName"`
	BundleName           string                 `json:"bundleName"`
	Namespace            string                 `json:"namespace"`
	ModifiedResources    []fleet.ModifiedStatus `json:"modifiedResources"`
	NonReadyResources    []fleet.NonReadyStatus `json:"nonReadyResources,omitempty"`
}

type BundleDiffOutput struct {
	BundleName            string       `json:"bundleName,omitempty"`
	Namespace             string       `json:"namespace"`
	BundleDeploymentDiffs []DiffOutput `json:"bundleDeploymentDiffs"`
}

func (d *BundleDiff) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := d.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}

	return nil
}

func (d *BundleDiff) Run(cmd *cobra.Command, args []string) error {
	if d.JSON && d.FleetYAML {
		return fmt.Errorf("cannot specify both --json and --fleet-yaml")
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get k8s config: %w", err)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	if d.FleetYAML && d.BundleDeployment == "" {
		return fmt.Errorf("--fleet-yaml requires --bundle-deployment to be specified")
	}

	diffs := []DiffOutput{}

	switch {
	case d.BundleDeployment != "":
		diff, err := d.getBundleDeploymentDiff(ctx, k8sClient, d.Namespace, d.BundleDeployment)
		if err != nil {
			return err
		}
		if diff != nil {
			diffs = append(diffs, *diff)
		}
	case d.Bundle != "":
		diffs, err = d.getBundleDeploymentDiffsForBundle(ctx, k8sClient, d.Namespace, d.Bundle)
		if err != nil {
			return err
		}
	default:
		diffs, err = d.getAllBundleDiffs(ctx, k8sClient, d.Namespace)
		if err != nil {
			return err
		}
	}

	if len(diffs) == 0 {
		if !d.JSON && !d.FleetYAML {
			fmt.Fprintln(cmd.OutOrStdout(), "No modified resources found.")
			return nil
		}
		if d.JSON {
			return d.encodeJSON(cmd.OutOrStdout(), diffs)
		}
		if d.FleetYAML {
			return d.printFleetYAML(ctx, k8sClient, cmd.OutOrStdout(), diffs)
		}
		return nil
	}

	if d.JSON {
		return d.encodeJSON(cmd.OutOrStdout(), diffs)
	}

	if d.FleetYAML {
		return d.printFleetYAML(ctx, k8sClient, cmd.OutOrStdout(), diffs)
	}

	return d.printTextOutput(cmd.OutOrStdout(), diffs)
}

func (d *BundleDiff) encodeJSON(out io.Writer, diffs []DiffOutput) error {
	output := BundleDiffOutput{
		Namespace:             d.Namespace,
		BundleDeploymentDiffs: diffs,
	}
	if d.Bundle != "" {
		output.BundleName = d.Bundle
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// patchOperation represents a JSON Patch operation
type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// resourceKey returns a string representation of a resource for logging
func resourceKey(kind, apiVersion, namespace, name string) string {
	return fmt.Sprintf("%s.%s %s/%s", kind, apiVersion, namespace, name)
}

// escapeJSONPointerToken escapes a token for use in a JSON Pointer path.
// According to RFC 6901, ~ must be escaped as ~0 and / must be escaped as ~1.
func escapeJSONPointerToken(token string) string {
	// Replace ~ first to avoid double-escaping
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return token
}

// convertMergePatchToRemoveOps converts a JSON Merge Patch object to JSON Patch remove operations
// This allows users to ignore drift on fields that differ between desired and deployed resources
func convertMergePatchToRemoveOps(patch map[string]interface{}, basePath string) []patchOperation {
	var ops []patchOperation

	// Sort keys for deterministic output order
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := patch[key]
		escapedKey := escapeJSONPointerToken(key)
		path := basePath + "/" + escapedKey

		if nested, ok := value.(map[string]interface{}); ok {
			ops = append(ops, convertMergePatchToRemoveOps(nested, path)...)
		} else {
			// Both null and non-null values should generate remove operations
			// Remove = "ignore this field in drift detection"
			// This accepts any drift on this field, not just a specific value
			ops = append(ops, patchOperation{
				Op:   "remove",
				Path: path,
			})
		}
	}

	return ops
}

// mergeComparePatches merges new comparePatches with existing ones, avoiding duplicates.
// This preserves any user-configured comparePatches while adding new ones from drift detection.
func mergeComparePatches(existing, new []fleet.ComparePatch) []fleet.ComparePatch {
	patchMap := make(map[string]fleet.ComparePatch)

	// Key function for resource identity
	resourceKey := func(p fleet.ComparePatch) string {
		return fmt.Sprintf("%s|%s|%s|%s", p.APIVersion, p.Kind, p.Namespace, p.Name)
	}

	for _, patch := range existing {
		key := resourceKey(patch)
		patchMap[key] = patch
	}

	// Merge new patches, combining operations for same resource
	for _, newPatch := range new {
		key := resourceKey(newPatch)
		if existingPatch, found := patchMap[key]; found {
			merged := existingPatch
			opSet := make(map[string]bool)
			for _, op := range existingPatch.Operations {
				opKey := fmt.Sprintf("%s|%s|%s", op.Op, op.Path, op.Value)
				opSet[opKey] = true
			}
			for _, op := range newPatch.Operations {
				opKey := fmt.Sprintf("%s|%s|%s", op.Op, op.Path, op.Value)
				if !opSet[opKey] {
					merged.Operations = append(merged.Operations, op)
				}
			}
			pointerSet := make(map[string]bool)
			for _, pointer := range existingPatch.JsonPointers {
				pointerSet[pointer] = true
			}
			for _, pointer := range newPatch.JsonPointers {
				if !pointerSet[pointer] {
					merged.JsonPointers = append(merged.JsonPointers, pointer)
				}
			}
			patchMap[key] = merged
		} else {
			patchMap[key] = newPatch
		}
	}

	// Convert map back to slice, sorted for deterministic output
	var result []fleet.ComparePatch
	keys := make([]string, 0, len(patchMap))
	for k := range patchMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		result = append(result, patchMap[k])
	}

	return result
}

func (d *BundleDiff) printFleetYAML(ctx context.Context, k8sClient client.Client, out io.Writer, diffs []DiffOutput) error {
	if len(diffs) != 1 || diffs[0].BundleName == "" {
		return fmt.Errorf("failed to find bundle deployment %s", d.BundleDeployment)
	}

	comparePatches := []fleet.ComparePatch{}

	for _, diff := range diffs {
		for _, mod := range diff.ModifiedResources {
			if mod.Patch == "" {
				continue
			}

			resKey := resourceKey(mod.Kind, mod.APIVersion, mod.Namespace, mod.Name)

			// Fleet always produces JSON Merge Patch format from strategic merge or JSON merge patch
			var mergePatch map[string]interface{}
			if err := json.Unmarshal([]byte(mod.Patch), &mergePatch); err != nil {
				ctrl.Log.Error(err, "failed to parse patch, skipping resource", "resource", resKey)
				continue
			}

			// Convert JSON Merge Patch to JSON Patch remove operations
			patchOps := convertMergePatchToRemoveOps(mergePatch, "")

			operations := make([]fleet.Operation, 0, len(patchOps))
			// Record JSON pointers so the ignore normalizer can skip these paths directly.
			jsonPointers := make([]string, 0, len(patchOps))
			for _, p := range patchOps {
				if p.Op == "" {
					continue
				}
				if p.Path != "" {
					jsonPointers = append(jsonPointers, p.Path)
				}

				operations = append(operations, fleet.Operation{
					Op:   p.Op,
					Path: p.Path,
				})
			}

			if len(operations) > 0 {
				comparePatches = append(comparePatches, fleet.ComparePatch{
					APIVersion:   mod.APIVersion,
					Kind:         mod.Kind,
					Name:         mod.Name,
					Namespace:    mod.Namespace,
					Operations:   operations,
					JsonPointers: jsonPointers,
				})
			}
		}
	}

	if len(comparePatches) == 0 {
		return nil
	}

	return d.outputFleetYAMLDiff(ctx, k8sClient, out, diffs[0], comparePatches)
}

// outputFleetYAMLDiff outputs a fleet.yaml diff snippet that can be added to a GitRepo's fleet.yaml.
// This allows users to integrate detected drift as permanent ignores in their GitOps workflow.
func (d *BundleDiff) outputFleetYAMLDiff(ctx context.Context, k8sClient client.Client, out io.Writer, diff DiffOutput, comparePatches []fleet.ComparePatch) error {
	var bd fleet.BundleDeployment
	bdNamespace := diff.Namespace
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: bdNamespace,
		Name:      diff.BundleDeploymentName,
	}, &bd); err != nil {
		return fmt.Errorf("failed to get BundleDeployment %s/%s: %w", bdNamespace, diff.BundleDeploymentName, err)
	}

	bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
	if bundleNamespace == "" {
		bundleNamespace = d.Namespace
		if bundleNamespace == "" {
			bundleNamespace = "fleet-local"
		}
	}

	// Get the Bundle to merge with existing comparePatches if any
	var bundle fleet.Bundle
	err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: bundleNamespace,
		Name:      diff.BundleName,
	}, &bundle)
	if err != nil {
		return fmt.Errorf("failed to get Bundle %s/%s: %w", bundleNamespace, diff.BundleName, err)
	}

	// Merge new comparePatches with existing ones from the Bundle
	var existingPatches []fleet.ComparePatch
	if bundle.Spec.Diff != nil && bundle.Spec.Diff.ComparePatches != nil {
		existingPatches = bundle.Spec.Diff.ComparePatches
	}
	mergedPatches := mergeComparePatches(existingPatches, comparePatches)

	// Keep yaml field order stable for readability and test fixtures.
	type comparePatchYAML struct {
		APIVersion   string            `json:"apiVersion,omitempty"`
		Kind         string            `json:"kind,omitempty"`
		Name         string            `json:"name,omitempty"`
		Namespace    string            `json:"namespace,omitempty"`
		JsonPointers []string          `json:"jsonPointers,omitempty"`
		Operations   []fleet.Operation `json:"operations,omitempty"`
	}

	patches := make([]comparePatchYAML, 0, len(mergedPatches))
	for _, patch := range mergedPatches {
		patches = append(patches, comparePatchYAML{
			APIVersion:   patch.APIVersion,
			Kind:         patch.Kind,
			Name:         patch.Name,
			Namespace:    patch.Namespace,
			JsonPointers: patch.JsonPointers,
			Operations:   patch.Operations,
		})
	}

	// Output just the diff section as a fleet.yaml snippet
	// We marshal DiffOptions directly and add the "diff:" prefix manually
	// to ensure lowercase field names matching fleet.yaml conventions
	diffOptions := struct {
		ComparePatches []comparePatchYAML `json:"comparePatches,omitempty"`
	}{
		ComparePatches: patches,
	}

	yamlOutput, err := yaml.Marshal(&diffOptions)
	if err != nil {
		return fmt.Errorf("failed to marshal diff options: %w", err)
	}

	// Format as fleet.yaml snippet with proper indentation
	output := "diff:\n"
	for _, line := range strings.Split(string(yamlOutput), "\n") {
		if line != "" {
			output += "  " + line + "\n"
		}
	}

	fmt.Fprint(out, output)
	return nil
}

func (d *BundleDiff) getBundleDeploymentDiff(ctx context.Context, k8sClient client.Client, namespace, name string) (*DiffOutput, error) {
	var bd fleet.BundleDeployment
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &bd)
	if err != nil {
		return nil, fmt.Errorf("failed to get BundleDeployment %s/%s: %w", namespace, name, err)
	}

	if len(bd.Status.ModifiedStatus) == 0 && len(bd.Status.NonReadyStatus) == 0 {
		return nil, nil
	}

	bundleName := bd.Labels["fleet.cattle.io/bundle-name"]
	return &DiffOutput{
		BundleDeploymentName: bd.Name,
		BundleName:           bundleName,
		Namespace:            bd.Namespace,
		ModifiedResources:    bd.Status.ModifiedStatus,
		NonReadyResources:    bd.Status.NonReadyStatus,
	}, nil
}

func (d *BundleDiff) getBundleDeploymentDiffsForBundle(ctx context.Context, k8sClient client.Client, namespace, bundleName string) ([]DiffOutput, error) {
	return d.listBundleDeploymentDiffs(ctx, k8sClient, namespace, client.MatchingLabels{
		"fleet.cattle.io/bundle-name": bundleName,
	})
}

func (d *BundleDiff) getAllBundleDeploymentDiffs(ctx context.Context, k8sClient client.Client, namespace string) ([]DiffOutput, error) {
	return d.listBundleDeploymentDiffs(ctx, k8sClient, namespace)
}

func (d *BundleDiff) getAllBundleDiffs(ctx context.Context, k8sClient client.Client, namespace string) ([]DiffOutput, error) {
	return d.getAllBundleDeploymentDiffs(ctx, k8sClient, namespace)
}

func (d *BundleDiff) listBundleDeploymentDiffs(ctx context.Context, k8sClient client.Client, namespace string, opts ...client.ListOption) ([]DiffOutput, error) {
	var bdList fleet.BundleDeploymentList
	// Search across all namespaces by default (when using the default fleet-local namespace)
	// Only restrict to a specific namespace when explicitly provided and different from default,
	// or when querying a specific BundleDeployment by name
	if (namespace != "" && namespace != "fleet-local") || d.BundleDeployment != "" {
		opts = append([]client.ListOption{client.InNamespace(namespace)}, opts...)
	}
	// Otherwise search across all namespaces to find BundleDeployments (which live in cluster namespaces)

	if err := k8sClient.List(ctx, &bdList, opts...); err != nil {
		return nil, fmt.Errorf("failed to list BundleDeployments: %w", err)
	}

	var diffs []DiffOutput
	for _, bd := range bdList.Items {
		if len(bd.Status.ModifiedStatus) > 0 || len(bd.Status.NonReadyStatus) > 0 {
			bundleName := bd.Labels["fleet.cattle.io/bundle-name"]
			diffs = append(diffs, DiffOutput{
				BundleDeploymentName: bd.Name,
				BundleName:           bundleName,
				Namespace:            bd.Namespace,
				ModifiedResources:    bd.Status.ModifiedStatus,
				NonReadyResources:    bd.Status.NonReadyStatus,
			})
		}
	}

	return diffs, nil
}

func (d *BundleDiff) printTextOutput(out io.Writer, diffs []DiffOutput) error {
	if d.BundleDeployment == "" {
		return d.printGroupedByBundle(out, diffs)
	}

	for i, diff := range diffs {
		if i > 0 {
			fmt.Fprintln(out, "")
		}

		fmt.Fprintf(out, "BundleDeployment: %s/%s\n", diff.Namespace, diff.BundleDeploymentName)
		if diff.BundleName != "" {
			fmt.Fprintf(out, "Bundle: %s\n", diff.BundleName)
		}
		fmt.Fprintln(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		if len(diff.ModifiedResources) > 0 {
			fmt.Fprintln(out, "\nModified Resources:")
			for _, mod := range diff.ModifiedResources {
				d.printModifiedResource(out, mod)
			}
		}

		if len(diff.NonReadyResources) > 0 {
			fmt.Fprintln(out, "\nNon-Ready Resources:")
			for _, nr := range diff.NonReadyResources {
				d.printNonReadyResource(out, nr)
			}
		}
	}
	return nil
}

func (d *BundleDiff) printGroupedByBundle(out io.Writer, diffs []DiffOutput) error {
	bundleMap := make(map[string][]DiffOutput)
	for _, diff := range diffs {
		bundleName := diff.BundleName
		if bundleName == "" {
			bundleName = "(unknown)"
		}
		bundleMap[bundleName] = append(bundleMap[bundleName], diff)
	}

	// Sort bundle names for deterministic output
	bundleNames := make([]string, 0, len(bundleMap))
	for bundleName := range bundleMap {
		bundleNames = append(bundleNames, bundleName)
	}
	sort.Strings(bundleNames)

	isFirst := true
	for _, bundleName := range bundleNames {
		bundleDiffs := bundleMap[bundleName]
		if !isFirst {
			fmt.Fprintln(out, "")
		}
		isFirst = false

		fmt.Fprintf(out, "Bundle: %s\n", bundleName)
		fmt.Fprintf(out, "BundleDeployments with diffs: %d\n", len(bundleDiffs))
		fmt.Fprintln(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		for _, diff := range bundleDiffs {
			fmt.Fprintf(out, "\n  BundleDeployment: %s/%s\n", diff.Namespace, diff.BundleDeploymentName)

			if len(diff.ModifiedResources) > 0 {
				fmt.Fprintf(out, "  Modified Resources (%d):\n", len(diff.ModifiedResources))
				for _, mod := range diff.ModifiedResources {
					d.printModifiedResourceIndented(out, mod, "    ")
				}
			}

			if len(diff.NonReadyResources) > 0 {
				fmt.Fprintf(out, "  Non-Ready Resources (%d):\n", len(diff.NonReadyResources))
				for _, nr := range diff.NonReadyResources {
					d.printNonReadyResourceIndented(out, nr, "    ")
				}
			}
		}
	}

	return nil
}

func formatResourceID(kind, apiVersion, namespace, name string) string {
	resourceName := name
	if namespace != "" {
		resourceName = namespace + "/" + name
	}
	return fmt.Sprintf("%s.%s %s", kind, apiVersion, resourceName)
}

func (d *BundleDiff) printModifiedResource(out io.Writer, mod fleet.ModifiedStatus) {
	fmt.Fprintf(out, "\n  Resource: %s\n", formatResourceID(mod.Kind, mod.APIVersion, mod.Namespace, mod.Name))

	switch {
	case mod.Create:
		if mod.Exist {
			fmt.Fprintln(out, "  Status: Resource exists but is not owned by Fleet")
		} else {
			fmt.Fprintln(out, "  Status: Resource is missing (should be created)")
		}
	case mod.Delete:
		fmt.Fprintln(out, "  Status: Extra resource (should be deleted)")
	case mod.Patch != "":
		fmt.Fprintln(out, "  Status: Modified")
		fmt.Fprintf(out, "  Patch:\n%s\n", d.formatPatch(mod.Patch))
	}
}

func (d *BundleDiff) printNonReadyResource(out io.Writer, nr fleet.NonReadyStatus) {
	fmt.Fprintf(out, "\n  Resource: %s\n", formatResourceID(nr.Kind, nr.APIVersion, nr.Namespace, nr.Name))
	fmt.Fprintln(out, "  Status: Not Ready")
	if nr.Summary.State != "" {
		fmt.Fprintf(out, "  Summary: %s\n", nr.Summary.String())
	}
}

func (d *BundleDiff) printModifiedResourceIndented(out io.Writer, mod fleet.ModifiedStatus, indent string) {
	fmt.Fprintf(out, "%sResource: %s\n", indent, formatResourceID(mod.Kind, mod.APIVersion, mod.Namespace, mod.Name))

	switch {
	case mod.Create:
		if mod.Exist {
			fmt.Fprintf(out, "%sStatus: Resource exists but is not owned by Fleet\n", indent)
		} else {
			fmt.Fprintf(out, "%sStatus: Resource is missing (should be created)\n", indent)
		}
	case mod.Delete:
		fmt.Fprintf(out, "%sStatus: Extra resource (should be deleted)\n", indent)
	case mod.Patch != "":
		fmt.Fprintf(out, "%sStatus: Modified\n", indent)
		fmt.Fprintf(out, "%sPatch:\n%s\n", indent, d.formatPatchWithIndent(mod.Patch, indent+"  "))
	}
}

func (d *BundleDiff) printNonReadyResourceIndented(out io.Writer, nr fleet.NonReadyStatus, indent string) {
	fmt.Fprintf(out, "%sResource: %s\n", indent, formatResourceID(nr.Kind, nr.APIVersion, nr.Namespace, nr.Name))
	fmt.Fprintf(out, "%sStatus: Not Ready\n", indent)
	if nr.Summary.State != "" {
		fmt.Fprintf(out, "%sSummary: %s\n", indent, nr.Summary.String())
	}
}

func (d *BundleDiff) formatPatch(patch string) string {
	var patchObj interface{}
	if err := json.Unmarshal([]byte(patch), &patchObj); err != nil {
		return "    " + patch
	}

	prettyJSON, err := json.MarshalIndent(patchObj, "    ", "  ")
	if err != nil {
		return "    " + patch
	}

	// json.MarshalIndent prefix only applies to lines after the first, so add it manually
	return "    " + string(prettyJSON)
}

func (d *BundleDiff) formatPatchWithIndent(patch string, indent string) string {
	var patchObj interface{}
	if err := json.Unmarshal([]byte(patch), &patchObj); err != nil {
		return indent + patch
	}

	prettyJSON, err := json.MarshalIndent(patchObj, indent, "  ")
	if err != nil {
		return indent + patch
	}

	// json.MarshalIndent prefix only applies to lines after the first, so add it manually
	return indent + string(prettyJSON)
}
