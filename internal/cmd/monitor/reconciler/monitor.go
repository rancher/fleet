// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var globalStatsTracker = NewStatsTracker()

// GetStatsTracker returns the global stats tracker
func GetStatsTracker() *StatsTracker {
	return globalStatsTracker
}

// recordEvent records an event in statistics (always, regardless of mode)
func recordEvent(resourceType, namespace, name string, eventType EventType) {
	globalStatsTracker.RecordEvent(resourceType, namespace, name, eventType)
}

// logSpecChange logs the differences in spec between old and new objects
// detailedLogs parameter controls whether to emit detailed log lines
// eventFilters parameter controls which event types to show
func logSpecChange(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, oldSpec, newSpec interface{}, oldGen, newGen int64) {
	if oldGen == newGen {
		return
	}

	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeGenerationChange)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeGenerationChange) {
		diff := cmp.Diff(oldSpec, newSpec)
		if diff != "" {
			logger.Info("Spec changed - Generation update detected",
				"event", "generation-change",
				"oldGeneration", oldGen,
				"newGeneration", newGen,
				"specDiff", diff,
			)
		} else {
			logger.Info("Generation changed but spec appears identical",
				"event", "generation-change",
				"oldGeneration", oldGen,
				"newGeneration", newGen,
			)
		}
	}
}

// logStatusChange logs differences in status between old and new objects
func logStatusChange(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, oldStatus, newStatus interface{}) {
	if equality.Semantic.DeepEqual(oldStatus, newStatus) {
		return
	}

	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeStatusChange)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeStatusChange) {
		diff := cmp.Diff(oldStatus, newStatus)

		logger.Info("Status changed",
			"event", "status-change",
			"diff", diff,
		)
	}
}

// logResourceVersionChangeWithMetadata logs resource version changes and checks for metadata differences
func logResourceVersionChangeWithMetadata(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, oldObj, newObj client.Object) {
	oldRV := oldObj.GetResourceVersion()
	newRV := newObj.GetResourceVersion()

	if oldRV == newRV {
		return
	}

	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeResourceVersionChange)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeResourceVersionChange) {
		// Check for specific metadata changes
		var metadataChanges []string
		var diffs []string

		// Check finalizers
		oldFinalizers := oldObj.GetFinalizers()
		newFinalizers := newObj.GetFinalizers()
		if !equality.Semantic.DeepEqual(oldFinalizers, newFinalizers) {
			metadataChanges = append(metadataChanges, "finalizers")
			diff := cmp.Diff(oldFinalizers, newFinalizers)
			diffs = append(diffs, "Finalizers:\n"+diff)
		}

		// Check owner references
		oldOwners := oldObj.GetOwnerReferences()
		newOwners := newObj.GetOwnerReferences()
		if !equality.Semantic.DeepEqual(oldOwners, newOwners) {
			metadataChanges = append(metadataChanges, "ownerReferences")
			diff := cmp.Diff(oldOwners, newOwners)
			diffs = append(diffs, "OwnerReferences:\n"+diff)
		}

		// Check managed fields (common with Server-Side Apply).
		// Use managedFieldsDiff as the sole detector: equality.Semantic.DeepEqual
		// on slices is order-sensitive, so a mere reordering of SSA entries would
		// trigger a false "changed" detection while producing an empty diff.
		if managedDiff := managedFieldsDiff(oldObj.GetManagedFields(), newObj.GetManagedFields()); managedDiff != "" {
			metadataChanges = append(metadataChanges, "managedFields")
			diffs = append(diffs, "ManagedFields:\n"+managedDiff)
		}

		reason := "cache sync or unknown metadata update"
		if len(metadataChanges) > 0 {
			// Format metadataChanges as comma-separated list
			var changeList string
			for i, change := range metadataChanges {
				if i > 0 {
					changeList += ", "
				}
				changeList += change
			}
			reason = "metadata update: " + changeList
		}

		logFields := []interface{}{
			"event", "resourceversion-change",
			"oldResourceVersion", oldRV,
			"newResourceVersion", newRV,
			"reason", reason,
		}

		if len(metadataChanges) > 0 {
			logFields = append(logFields, "metadataChanges", metadataChanges)
			if len(diffs) > 0 {
				logFields = append(logFields, "diff", strings.Join(diffs, "\n"))
			}
		}

		logger.Info("Resource version changed", logFields...)
	}
}

// logAnnotationChange logs annotation changes
func logAnnotationChange(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, oldAnnotations, newAnnotations map[string]string) {
	if equality.Semantic.DeepEqual(oldAnnotations, newAnnotations) {
		return
	}

	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeAnnotationChange)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeAnnotationChange) {
		diff := cmp.Diff(oldAnnotations, newAnnotations)
		logger.Info("Annotations changed",
			"event", "annotation-change",
			"diff", diff,
		)
	}
}

// logLabelChange logs label changes
func logLabelChange(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, oldLabels, newLabels map[string]string) {
	if equality.Semantic.DeepEqual(oldLabels, newLabels) {
		return
	}

	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeLabelChange)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeLabelChange) {
		diff := cmp.Diff(oldLabels, newLabels)
		logger.Info("Labels changed",
			"event", "label-change",
			"diff", diff,
		)
	}
}

// logRelatedResourceTrigger logs when a reconciliation is triggered by a related resource
func logRelatedResourceTrigger(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLogTrigger() bool }, resourceType, namespace, name string, triggerType, triggerName, triggerNamespace string) {
	// Always record in stats with breakdown by trigger type
	globalStatsTracker.RecordTrigger(resourceType, namespace, name, triggerType)

	// Only log details if detailed mode enabled AND triggered-by events are enabled
	if detailedLogs && eventFilters.ShouldLogTrigger() {
		logger.Info("Triggered by related resource change",
			"event", "related-resource-trigger",
			"triggerResourceType", triggerType,
			"triggerResourceName", triggerName,
			"triggerResourceNamespace", triggerNamespace,
		)
	}
}

// logDeletion logs when a resource is being deleted
func logDeletion(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, deletionTimestamp string) {
	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeDeletion)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeDeletion) {
		logger.Info("Resource deletion detected",
			"event", "deletion",
			"deletionTimestamp", deletionTimestamp,
		)
	}
}

// logNotFound logs when a resource is not found (deleted)
func logNotFound(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string) {
	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeNotFound)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeNotFound) {
		logger.Info("Resource not found - likely deleted",
			"event", "not-found",
		)
	}
}

// logCreate logs first observation of a resource
func logCreate(logger logr.Logger, detailedLogs bool, eventFilters interface{ ShouldLog(EventType) bool }, resourceType, namespace, name string, generation int64, resourceVersion string) {
	// Always record in stats
	recordEvent(resourceType, namespace, name, EventTypeCreate)

	// Only log details if detailed mode enabled AND event type is enabled
	if detailedLogs && eventFilters.ShouldLog(EventTypeCreate) {
		logger.Info("First observation of resource",
			"event", "create",
			"generation", generation,
			"resourceVersion", resourceVersion,
		)
	}
}

// managedFieldsDiff returns a human-readable summary of what changed in managedFields.
// It identifies which field managers were added, removed, or changed, and for changed
// managers it shows a diff of their owned fields (parsed from FieldsV1 JSON).
func managedFieldsDiff(old, new []metav1.ManagedFieldsEntry) string {
	type entryKey struct {
		Manager     string
		Operation   metav1.ManagedFieldsOperationType
		Subresource string
	}

	oldMap := make(map[entryKey]metav1.ManagedFieldsEntry, len(old))
	for _, e := range old {
		oldMap[entryKey{e.Manager, e.Operation, e.Subresource}] = e
	}

	newMap := make(map[entryKey]metav1.ManagedFieldsEntry, len(new))
	for _, e := range new {
		newMap[entryKey{e.Manager, e.Operation, e.Subresource}] = e
	}

	var added, removed, changed []string
	var fieldDiffs []string

	for k, newEntry := range newMap {
		oldEntry, exists := oldMap[k]
		if !exists {
			added = append(added, fmt.Sprintf("%s(%s)", k.Manager, k.Operation))
			continue
		}
		if !equality.Semantic.DeepEqual(newEntry, oldEntry) {
			label := fmt.Sprintf("%s(%s)", k.Manager, k.Operation)
			changed = append(changed, label)
			diff := diffFieldsV1(oldEntry.FieldsV1, newEntry.FieldsV1)
			if diff != "" {
				fieldDiffs = append(fieldDiffs, fmt.Sprintf("[%s]:\n%s", label, diff))
			}
		}
	}

	for k := range oldMap {
		if _, exists := newMap[k]; !exists {
			removed = append(removed, fmt.Sprintf("%s(%s)", k.Manager, k.Operation))
		}
	}

	var sb strings.Builder
	if len(added) > 0 {
		sb.WriteString("added: " + strings.Join(added, ", ") + "\n")
	}
	if len(removed) > 0 {
		sb.WriteString("removed: " + strings.Join(removed, ", ") + "\n")
	}
	if len(changed) > 0 {
		sb.WriteString("changed: " + strings.Join(changed, ", ") + "\n")
	}
	for _, fd := range fieldDiffs {
		sb.WriteString(fd + "\n")
	}

	return sb.String()
}

// diffFieldsV1 diffs two FieldsV1 values by parsing their raw JSON.
// Falls back to an empty string if both are nil or identical.
func diffFieldsV1(old, new *metav1.FieldsV1) string {
	if old == nil && new == nil {
		return ""
	}
	var oldParsed, newParsed interface{}
	if old != nil {
		_ = json.Unmarshal(old.Raw, &oldParsed)
	}
	if new != nil {
		_ = json.Unmarshal(new.Raw, &newParsed)
	}
	return cmp.Diff(oldParsed, newParsed)
}
