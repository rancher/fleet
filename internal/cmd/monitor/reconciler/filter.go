// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"fmt"
	"regexp"
)

// EventTypeFilters controls which event types produce detailed logs
type EventTypeFilters struct {
	GenerationChange      bool // generation-change events
	StatusChange          bool // status-change events
	AnnotationChange      bool // annotation-change events
	LabelChange           bool // label-change events
	ResourceVersionChange bool // resourceversion-change events
	Deletion              bool // deletion events
	NotFound              bool // not-found events
	Create                bool // create events
	TriggeredBy           bool // triggered-by events
}

// IsEmpty returns true if no filters were explicitly set (use all events)
func (f EventTypeFilters) IsEmpty() bool {
	return !f.GenerationChange &&
		!f.StatusChange &&
		!f.AnnotationChange &&
		!f.LabelChange &&
		!f.ResourceVersionChange &&
		!f.Deletion &&
		!f.NotFound &&
		!f.Create &&
		!f.TriggeredBy
}

// ShouldLog returns true if the given event type should produce detailed logs
func (f EventTypeFilters) ShouldLog(eventType EventType) bool {
	// If no filters set, log everything (backwards compatible)
	if f.IsEmpty() {
		return true
	}

	switch eventType {
	case EventTypeGenerationChange:
		return f.GenerationChange
	case EventTypeStatusChange:
		return f.StatusChange
	case EventTypeAnnotationChange:
		return f.AnnotationChange
	case EventTypeLabelChange:
		return f.LabelChange
	case EventTypeResourceVersionChange:
		return f.ResourceVersionChange
	case EventTypeDeletion:
		return f.Deletion
	case EventTypeNotFound:
		return f.NotFound
	case EventTypeCreate:
		return f.Create
	default:
		return true // Unknown event types always logged
	}
}

// ShouldLogTrigger returns true if triggered-by events should produce detailed logs
func (f EventTypeFilters) ShouldLogTrigger() bool {
	if f.IsEmpty() {
		return true
	}
	return f.TriggeredBy
}

// ResourceFilter defines namespace/name patterns for filtering monitored resources
type ResourceFilter struct {
	// NamespacePattern is a regular expression for matching resource namespaces
	// Empty string matches all namespaces
	NamespacePattern string

	// NamePattern is a regular expression for matching resource names
	// Empty string matches all names
	NamePattern string

	// Compiled regex patterns (internal use)
	namespaceRegex *regexp.Regexp
	nameRegex      *regexp.Regexp
}

// Matches returns true if the resource namespace and name match the filter
// If filter is nil or both patterns are empty, returns true (match all)
func (f *ResourceFilter) Matches(namespace, name string) bool {
	if f == nil {
		return true
	}

	// If both patterns are empty, match everything (backwards compatible)
	if f.NamespacePattern == "" && f.NamePattern == "" {
		return true
	}

	// Empty patterns match everything
	namespaceMatch := f.NamespacePattern == "" || (f.namespaceRegex != nil && f.namespaceRegex.MatchString(namespace))
	nameMatch := f.NamePattern == "" || (f.nameRegex != nil && f.nameRegex.MatchString(name))

	return namespaceMatch && nameMatch
}

// Compile prepares the regex patterns for use
// Returns error if any pattern is invalid
func (f *ResourceFilter) Compile() error {
	if f == nil {
		return nil
	}

	var err error
	if f.NamespacePattern != "" {
		f.namespaceRegex, err = regexp.Compile(f.NamespacePattern)
		if err != nil {
			return fmt.Errorf("invalid namespace pattern %q: %w", f.NamespacePattern, err)
		}
	}

	if f.NamePattern != "" {
		f.nameRegex, err = regexp.Compile(f.NamePattern)
		if err != nil {
			return fmt.Errorf("invalid name pattern %q: %w", f.NamePattern, err)
		}
	}

	return nil
}
