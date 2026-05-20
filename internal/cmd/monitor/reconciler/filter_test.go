// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import "testing"

func TestEventTypeFilters_IsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		filters EventTypeFilters
		want    bool
	}{
		{name: "zero value", filters: EventTypeFilters{}, want: true},
		{name: "GenerationChange set", filters: EventTypeFilters{GenerationChange: true}, want: false},
		{name: "StatusChange set", filters: EventTypeFilters{StatusChange: true}, want: false},
		{name: "AnnotationChange set", filters: EventTypeFilters{AnnotationChange: true}, want: false},
		{name: "LabelChange set", filters: EventTypeFilters{LabelChange: true}, want: false},
		{name: "ResourceVersionChange set", filters: EventTypeFilters{ResourceVersionChange: true}, want: false},
		{name: "Deletion set", filters: EventTypeFilters{Deletion: true}, want: false},
		{name: "NotFound set", filters: EventTypeFilters{NotFound: true}, want: false},
		{name: "Create set", filters: EventTypeFilters{Create: true}, want: false},
		{name: "TriggeredBy set", filters: EventTypeFilters{TriggeredBy: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventTypeFilters_ShouldLog_EmptyFiltersLogAll(t *testing.T) {
	f := EventTypeFilters{}
	eventTypes := []EventType{
		EventTypeGenerationChange,
		EventTypeStatusChange,
		EventTypeAnnotationChange,
		EventTypeLabelChange,
		EventTypeResourceVersionChange,
		EventTypeDeletion,
		EventTypeNotFound,
		EventTypeCreate,
	}
	for _, et := range eventTypes {
		if !f.ShouldLog(et) {
			t.Errorf("empty filters: ShouldLog(%q) = false, want true", et)
		}
	}
}

func TestEventTypeFilters_ShouldLog_SpecificFilters(t *testing.T) {
	tests := []struct {
		name      string
		filters   EventTypeFilters
		eventType EventType
		want      bool
	}{
		{
			name:      "GenerationChange enabled, query generation-change",
			filters:   EventTypeFilters{GenerationChange: true},
			eventType: EventTypeGenerationChange,
			want:      true,
		},
		{
			name:      "GenerationChange enabled, query status-change",
			filters:   EventTypeFilters{GenerationChange: true},
			eventType: EventTypeStatusChange,
			want:      false,
		},
		{
			name:      "StatusChange enabled",
			filters:   EventTypeFilters{StatusChange: true},
			eventType: EventTypeStatusChange,
			want:      true,
		},
		{
			name:      "AnnotationChange enabled",
			filters:   EventTypeFilters{AnnotationChange: true},
			eventType: EventTypeAnnotationChange,
			want:      true,
		},
		{
			name:      "LabelChange enabled",
			filters:   EventTypeFilters{LabelChange: true},
			eventType: EventTypeLabelChange,
			want:      true,
		},
		{
			name:      "ResourceVersionChange enabled",
			filters:   EventTypeFilters{ResourceVersionChange: true},
			eventType: EventTypeResourceVersionChange,
			want:      true,
		},
		{
			name:      "Deletion enabled",
			filters:   EventTypeFilters{Deletion: true},
			eventType: EventTypeDeletion,
			want:      true,
		},
		{
			name:      "NotFound enabled",
			filters:   EventTypeFilters{NotFound: true},
			eventType: EventTypeNotFound,
			want:      true,
		},
		{
			name:      "Create enabled",
			filters:   EventTypeFilters{Create: true},
			eventType: EventTypeCreate,
			want:      true,
		},
		{
			name:      "unknown event type always logged when filters set",
			filters:   EventTypeFilters{GenerationChange: true},
			eventType: EventType("unknown"),
			want:      true,
		},
		{
			name:      "multiple filters, only one matches",
			filters:   EventTypeFilters{GenerationChange: true, StatusChange: true},
			eventType: EventTypeAnnotationChange,
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.ShouldLog(tt.eventType); got != tt.want {
				t.Errorf("ShouldLog(%q) = %v, want %v", tt.eventType, got, tt.want)
			}
		})
	}
}

func TestEventTypeFilters_ShouldLogTrigger(t *testing.T) {
	tests := []struct {
		name    string
		filters EventTypeFilters
		want    bool
	}{
		{name: "empty filters log all", filters: EventTypeFilters{}, want: true},
		{name: "TriggeredBy true", filters: EventTypeFilters{TriggeredBy: true}, want: true},
		{name: "only other filters set, TriggeredBy false", filters: EventTypeFilters{GenerationChange: true}, want: false},
		{name: "TriggeredBy true with other filters", filters: EventTypeFilters{GenerationChange: true, TriggeredBy: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.ShouldLogTrigger(); got != tt.want {
				t.Errorf("ShouldLogTrigger() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceFilter_Compile(t *testing.T) {
	tests := []struct {
		name      string
		filter    *ResourceFilter
		wantError bool
	}{
		{name: "nil filter", filter: nil, wantError: false},
		{name: "empty filter", filter: &ResourceFilter{}, wantError: false},
		{name: "valid namespace pattern", filter: &ResourceFilter{NamespacePattern: "fleet-.*"}, wantError: false},
		{name: "valid name pattern", filter: &ResourceFilter{NamePattern: "my-app-.*"}, wantError: false},
		{name: "both valid patterns", filter: &ResourceFilter{NamespacePattern: "fleet-.*", NamePattern: "my-.*"}, wantError: false},
		{name: "invalid namespace pattern", filter: &ResourceFilter{NamespacePattern: "["}, wantError: true},
		{name: "invalid name pattern", filter: &ResourceFilter{NamePattern: "["}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Compile()
			if (err != nil) != tt.wantError {
				t.Errorf("Compile() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestResourceFilter_Matches(t *testing.T) {
	tests := []struct {
		name      string
		filter    *ResourceFilter
		namespace string
		resName   string
		want      bool
	}{
		{
			name:      "nil filter matches all",
			filter:    nil,
			namespace: "any-ns",
			resName:   "any-name",
			want:      true,
		},
		{
			name:      "empty filter matches all",
			filter:    &ResourceFilter{},
			namespace: "any-ns",
			resName:   "any-name",
			want:      true,
		},
		{
			name:      "namespace pattern matches",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*"},
			namespace: "fleet-local",
			resName:   "anything",
			want:      true,
		},
		{
			name:      "namespace pattern no match",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*"},
			namespace: "default",
			resName:   "anything",
			want:      false,
		},
		{
			name:      "name pattern matches",
			filter:    &ResourceFilter{NamePattern: "my-app"},
			namespace: "default",
			resName:   "my-app",
			want:      true,
		},
		{
			name:      "name pattern no match",
			filter:    &ResourceFilter{NamePattern: "^my-app$"},
			namespace: "default",
			resName:   "other-app",
			want:      false,
		},
		{
			name:      "both patterns match",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*", NamePattern: "my-.*"},
			namespace: "fleet-local",
			resName:   "my-bundle",
			want:      true,
		},
		{
			name:      "namespace matches but name does not",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*", NamePattern: "^my-.*"},
			namespace: "fleet-local",
			resName:   "other-bundle",
			want:      false,
		},
		{
			name:      "name matches but namespace does not",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*", NamePattern: "my-.*"},
			namespace: "default",
			resName:   "my-bundle",
			want:      false,
		},
		{
			name:      "regex partial match (substring)",
			filter:    &ResourceFilter{NamePattern: "bundle"},
			namespace: "default",
			resName:   "my-bundle",
			want:      true,
		},
		{
			name:      "only namespace pattern set, name matches all",
			filter:    &ResourceFilter{NamespacePattern: "fleet-.*"},
			namespace: "fleet-local",
			resName:   "any-name",
			want:      true,
		},
		{
			name:      "only name pattern set, namespace matches all",
			filter:    &ResourceFilter{NamePattern: "specific"},
			namespace: "any-ns",
			resName:   "specific",
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.filter != nil {
				if err := tt.filter.Compile(); err != nil {
					t.Fatalf("Compile() unexpected error: %v", err)
				}
			}
			got := tt.filter.Matches(tt.namespace, tt.resName)
			if got != tt.want {
				t.Errorf("Matches(%q, %q) = %v, want %v", tt.namespace, tt.resName, got, tt.want)
			}
		})
	}
}
