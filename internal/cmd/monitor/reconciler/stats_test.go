// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"strings"
	"testing"
	"time"
)

func TestResourceKey_String(t *testing.T) {
	tests := []struct {
		key  ResourceKey
		want string
	}{
		{
			key:  ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "name"},
			want: "ns/name",
		},
		{
			key:  ResourceKey{ResourceType: "Cluster", Namespace: "", Name: "cluster-a"},
			want: "cluster-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.key.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatsTracker_RecordEvent(t *testing.T) {
	tracker := NewStatsTracker()

	tracker.RecordEvent("Bundle", "ns", "name", EventTypeCreate)
	tracker.RecordEvent("Bundle", "ns", "name", EventTypeStatusChange)
	tracker.RecordEvent("Bundle", "ns", "name", EventTypeCreate) // repeated

	key := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "name"}
	stats := tracker.stats[key]
	if stats == nil {
		t.Fatal("expected stats entry for key")
	}
	if stats.Counts[EventTypeCreate] != 2 {
		t.Errorf("Create count = %d, want 2", stats.Counts[EventTypeCreate])
	}
	if stats.Counts[EventTypeStatusChange] != 1 {
		t.Errorf("StatusChange count = %d, want 1", stats.Counts[EventTypeStatusChange])
	}
	if stats.Total != 3 {
		t.Errorf("Total = %d, want 3", stats.Total)
	}
}

func TestStatsTracker_RecordEvent_DifferentResources(t *testing.T) {
	tracker := NewStatsTracker()

	tracker.RecordEvent("Bundle", "ns", "bundle-a", EventTypeCreate)
	tracker.RecordEvent("Bundle", "ns", "bundle-b", EventTypeCreate)
	tracker.RecordEvent("Cluster", "ns", "cluster-a", EventTypeStatusChange)

	if len(tracker.stats) != 3 {
		t.Errorf("expected 3 stats entries, got %d", len(tracker.stats))
	}
}

func TestStatsTracker_RecordTrigger(t *testing.T) {
	tracker := NewStatsTracker()

	tracker.RecordTrigger("Bundle", "ns", "name", "Cluster")
	tracker.RecordTrigger("Bundle", "ns", "name", "Cluster")
	tracker.RecordTrigger("Bundle", "ns", "name", "BundleDeployment")

	key := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "name"}
	stats := tracker.stats[key]
	if stats == nil {
		t.Fatal("expected stats entry for key")
	}
	if stats.TriggeredBy["Cluster"] != 2 {
		t.Errorf("Cluster trigger count = %d, want 2", stats.TriggeredBy["Cluster"])
	}
	if stats.TriggeredBy["BundleDeployment"] != 1 {
		t.Errorf("BundleDeployment trigger count = %d, want 1", stats.TriggeredBy["BundleDeployment"])
	}
	if stats.Total != 3 {
		t.Errorf("Total = %d, want 3", stats.Total)
	}
}

func TestStatsTracker_GetSummary(t *testing.T) {
	tracker := NewStatsTracker()

	tracker.RecordEvent("Bundle", "ns", "bundle-a", EventTypeCreate)
	tracker.RecordEvent("Bundle", "ns", "bundle-b", EventTypeCreate)
	tracker.RecordEvent("Cluster", "ns", "cluster-a", EventTypeStatusChange)

	summary := tracker.GetSummary()

	if len(summary.Summary) == 0 {
		t.Error("expected non-empty summary")
	}

	bundleStats := summary.Summary["Bundle"]
	if bundleStats == nil {
		t.Fatal("expected Bundle group in summary")
	}
	if len(bundleStats) != 2 {
		t.Errorf("expected 2 Bundle entries in summary, got %d", len(bundleStats))
	}

	clusterStats := summary.Summary["Cluster"]
	if clusterStats == nil {
		t.Fatal("expected Cluster group in summary")
	}

	if summary.Totals.TotalResourcesMonitored != 3 {
		t.Errorf("TotalResourcesMonitored = %d, want 3", summary.Totals.TotalResourcesMonitored)
	}
	if summary.Totals.TotalEvents != 3 {
		t.Errorf("TotalEvents = %d, want 3", summary.Totals.TotalEvents)
	}
}

func TestStatsTracker_GetSummary_DeepCopy(t *testing.T) {
	tracker := NewStatsTracker()
	tracker.RecordEvent("Bundle", "ns", "name", EventTypeCreate)

	summary := tracker.GetSummary()

	// Mutate the summary copy - should not affect tracker
	bundleStats := summary.Summary["Bundle"]["ns/name"]
	bundleStats.Counts[EventTypeCreate] = 999

	key := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "name"}
	if tracker.stats[key].Counts[EventTypeCreate] != 1 {
		t.Error("mutating summary copy should not affect original tracker")
	}
}

func TestStatsTracker_Reset(t *testing.T) {
	tracker := NewStatsTracker()
	tracker.RecordEvent("Bundle", "ns", "name", EventTypeCreate)

	before := tracker.lastSummaryTime
	time.Sleep(time.Millisecond)
	tracker.Reset()

	if len(tracker.stats) != 0 {
		t.Error("expected empty stats after Reset")
	}
	if !tracker.lastSummaryTime.After(before) {
		t.Error("expected lastSummaryTime to be updated after Reset")
	}
}

func TestStatsTracker_UpdateLastSummaryTime(t *testing.T) {
	tracker := NewStatsTracker()
	before := tracker.lastSummaryTime
	time.Sleep(time.Millisecond)
	tracker.UpdateLastSummaryTime()
	if !tracker.lastSummaryTime.After(before) {
		t.Error("expected lastSummaryTime to be updated")
	}
}

func TestSummary_ToJSON(t *testing.T) {
	tracker := NewStatsTracker()
	tracker.RecordEvent("Bundle", "ns", "name", EventTypeCreate)
	summary := tracker.GetSummary()

	jsonStr, err := summary.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error = %v", err)
	}
	if jsonStr == "" {
		t.Error("ToJSON() returned empty string")
	}
	if !strings.Contains(jsonStr, "Bundle") {
		t.Error("expected JSON to contain 'Bundle'")
	}
	if !strings.Contains(jsonStr, "total_events") {
		t.Error("expected JSON to contain 'total_events'")
	}
}

func TestSummary_ToJSONIndent(t *testing.T) {
	tracker := NewStatsTracker()
	tracker.RecordEvent("Cluster", "ns", "cluster-a", EventTypeStatusChange)
	summary := tracker.GetSummary()

	jsonStr, err := summary.ToJSONIndent()
	if err != nil {
		t.Fatalf("ToJSONIndent() error = %v", err)
	}
	if !strings.Contains(jsonStr, "\n") {
		t.Error("expected indented JSON to contain newlines")
	}
	if !strings.Contains(jsonStr, "Cluster") {
		t.Error("expected JSON to contain 'Cluster'")
	}
}

func TestResourceStats_MarshalJSON(t *testing.T) {
	rs := &ResourceStats{
		Counts: map[EventType]int64{
			EventTypeCreate:       3,
			EventTypeStatusChange: 1,
		},
		TriggeredBy: map[string]int64{
			"Cluster": 2,
		},
		Total: 4,
	}

	data, err := rs.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	jsonStr := string(data)
	if !strings.Contains(jsonStr, "create") {
		t.Error("expected JSON to contain 'create'")
	}
	if !strings.Contains(jsonStr, "total_events") {
		t.Error("expected JSON to contain 'total_events'")
	}
	if !strings.Contains(jsonStr, "triggered-by") {
		t.Error("expected JSON to contain 'triggered-by'")
	}
}

func TestResourceStats_MarshalJSON_ZeroCountsOmitted(t *testing.T) {
	rs := &ResourceStats{
		Counts: map[EventType]int64{
			EventTypeCreate: 0,
		},
		TriggeredBy: map[string]int64{},
		Total:       0,
	}

	data, err := rs.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	// Zero-count events should be omitted
	jsonStr := string(data)
	if strings.Contains(jsonStr, "create") {
		t.Error("expected zero-count events to be omitted from JSON")
	}
}
