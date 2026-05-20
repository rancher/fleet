// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"encoding/json"
	"sync"
	"time"
)

// EventType represents the type of reconciliation event
type EventType string

const (
	EventTypeGenerationChange      EventType = "generation-change"
	EventTypeStatusChange          EventType = "status-change"
	EventTypeAnnotationChange      EventType = "annotation-change"
	EventTypeLabelChange           EventType = "label-change"
	EventTypeResourceVersionChange EventType = "resourceversion-change"
	EventTypeDeletion              EventType = "deletion"
	EventTypeNotFound              EventType = "not-found"
	EventTypeCreate                EventType = "create"
)

// ResourceKey identifies a Kubernetes resource
type ResourceKey struct {
	ResourceType string // "Bundle", "Cluster", etc.
	Namespace    string
	Name         string
}

func (r ResourceKey) String() string {
	if r.Namespace == "" {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

// ResourceStats tracks event counts for a single resource
type ResourceStats struct {
	Counts      map[EventType]int64 `json:"-"` // Internal tracking
	TriggeredBy map[string]int64    `json:"triggered-by,omitempty"`
	Total       int64               `json:"total_events"`
}

// MarshalJSON implements custom JSON marshaling to flatten event counts
func (rs *ResourceStats) MarshalJSON() ([]byte, error) {
	// Create a map with all fields
	m := make(map[string]interface{})

	// Add simple event counts
	for eventType, count := range rs.Counts {
		if count > 0 {
			m[string(eventType)] = count
		}
	}

	// Add triggered-by breakdown
	if len(rs.TriggeredBy) > 0 {
		m["triggered-by"] = rs.TriggeredBy
	}

	// Add total
	m["total_events"] = rs.Total

	return json.Marshal(m)
}

// StatsTracker aggregates reconciliation statistics
type StatsTracker struct {
	mu              sync.RWMutex
	stats           map[ResourceKey]*ResourceStats
	startTime       time.Time
	lastSummaryTime time.Time
}

// NewStatsTracker creates a new statistics tracker
func NewStatsTracker() *StatsTracker {
	now := time.Now()
	return &StatsTracker{
		stats:           make(map[ResourceKey]*ResourceStats),
		startTime:       now,
		lastSummaryTime: now,
	}
}

// RecordEvent records an event for a resource
func (s *StatsTracker) RecordEvent(resourceType, namespace, name string, eventType EventType) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ResourceKey{
		ResourceType: resourceType,
		Namespace:    namespace,
		Name:         name,
	}

	if s.stats[key] == nil {
		s.stats[key] = &ResourceStats{
			Counts:      make(map[EventType]int64),
			TriggeredBy: make(map[string]int64),
		}
	}

	s.stats[key].Counts[eventType]++
	s.stats[key].Total++
}

// RecordTrigger records a trigger event with the trigger resource type
func (s *StatsTracker) RecordTrigger(resourceType, namespace, name string, triggerResourceType string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ResourceKey{
		ResourceType: resourceType,
		Namespace:    namespace,
		Name:         name,
	}

	if s.stats[key] == nil {
		s.stats[key] = &ResourceStats{
			Counts:      make(map[EventType]int64),
			TriggeredBy: make(map[string]int64),
		}
	}

	s.stats[key].TriggeredBy[triggerResourceType]++
	s.stats[key].Total++
}

// Summary represents a snapshot of statistics
type Summary struct {
	Timestamp       time.Time                            `json:"timestamp"`
	IntervalSeconds float64                              `json:"interval_seconds"`
	Summary         map[string]map[string]*ResourceStats `json:"summary"`
	Totals          Totals                               `json:"totals"`
}

// Totals represents aggregate statistics
type Totals struct {
	TotalResourcesMonitored int   `json:"total_resources_monitored"`
	TotalEvents             int64 `json:"total_events"`
}

// GetSummary returns a summary of all statistics
func (s *StatsTracker) GetSummary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	intervalSeconds := now.Sub(s.lastSummaryTime).Seconds()

	// Group by resource type
	grouped := make(map[string]map[string]*ResourceStats)
	totalEvents := int64(0)

	for key, stats := range s.stats {
		if grouped[key.ResourceType] == nil {
			grouped[key.ResourceType] = make(map[string]*ResourceStats)
		}

		// Deep copy stats to avoid race conditions
		statsCopy := &ResourceStats{
			Counts:      make(map[EventType]int64),
			TriggeredBy: make(map[string]int64),
			Total:       stats.Total,
		}
		for eventType, count := range stats.Counts {
			statsCopy.Counts[eventType] = count
		}
		for triggerType, count := range stats.TriggeredBy {
			statsCopy.TriggeredBy[triggerType] = count
		}

		grouped[key.ResourceType][key.String()] = statsCopy
		totalEvents += stats.Total
	}

	return Summary{
		Timestamp:       now,
		IntervalSeconds: intervalSeconds,
		Summary:         grouped,
		Totals: Totals{
			TotalResourcesMonitored: len(s.stats),
			TotalEvents:             totalEvents,
		},
	}
}

// Reset clears all statistics
func (s *StatsTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stats = make(map[ResourceKey]*ResourceStats)
	s.lastSummaryTime = time.Now()
}

// UpdateLastSummaryTime updates the last summary timestamp without resetting
func (s *StatsTracker) UpdateLastSummaryTime() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSummaryTime = time.Now()
}

// ToJSON converts summary to JSON string
func (s Summary) ToJSON() (string, error) {
	bytes, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// ToJSONIndent converts summary to indented JSON string for readability
func (s Summary) ToJSONIndent() (string, error) {
	bytes, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
