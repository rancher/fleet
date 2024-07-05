// +vendored argoproj/argo-cd/pkg/apis/application/v1alpha1/types.go
package resource

import (
	"encoding/json"

	"gopkg.in/yaml.v2"
)

// ResourceIgnoreDifferences contains resource filter and list of json paths which should be ignored during comparison with live state.
type ResourceIgnoreDifferences struct {
	Group        string   `json:"group,omitempty" protobuf:"bytes,1,opt,name=group"`
	Kind         string   `json:"kind" protobuf:"bytes,2,opt,name=kind"`
	Name         string   `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
	Namespace    string   `json:"namespace,omitempty" protobuf:"bytes,4,opt,name=namespace"`
	JSONPointers []string `json:"jsonPointers" protobuf:"bytes,5,opt,name=jsonPointers"`
}

// KnownTypeField contains mapping between CRD field and known Kubernetes type
type KnownTypeField struct {
	Field string `json:"field,omitempty" protobuf:"bytes,1,opt,name=field"`
	Type  string `json:"type,omitempty" protobuf:"bytes,2,opt,name=type"`
}

type OverrideIgnoreDiff struct {
	JSONPointers []string `json:"jsonPointers" protobuf:"bytes,1,rep,name=jSONPointers"`
}

// ResourceOverride holds configuration to customize resource diffing and health assessment
type ResourceOverride struct {
	HealthLua         string             `protobuf:"bytes,1,opt,name=healthLua"`
	Actions           string             `protobuf:"bytes,3,opt,name=actions"`
	IgnoreDifferences OverrideIgnoreDiff `protobuf:"bytes,2,opt,name=ignoreDifferences"`
	KnownTypeFields   []KnownTypeField   `protobuf:"bytes,4,opt,name=knownTypeFields"`
}

type rawResourceOverride struct {
	HealthLua         string           `json:"health.lua,omitempty"`
	Actions           string           `json:"actions,omitempty"`
	IgnoreDifferences string           `json:"ignoreDifferences,omitempty"`
	KnownTypeFields   []KnownTypeField `json:"knownTypeFields,omitempty"`
}

func (s *ResourceOverride) UnmarshalJSON(data []byte) error {
	raw := &rawResourceOverride{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.KnownTypeFields = raw.KnownTypeFields
	s.HealthLua = raw.HealthLua
	s.Actions = raw.Actions
	return yaml.Unmarshal([]byte(raw.IgnoreDifferences), &s.IgnoreDifferences)
}

func (s ResourceOverride) MarshalJSON() ([]byte, error) {
	ignoreDifferencesData, err := yaml.Marshal(s.IgnoreDifferences)
	if err != nil {
		return nil, err
	}
	raw := &rawResourceOverride{s.HealthLua, s.Actions, string(ignoreDifferencesData), s.KnownTypeFields}
	return json.Marshal(raw)
}

func (o *ResourceOverride) GetActions() (ResourceActions, error) {
	var actions ResourceActions
	err := yaml.Unmarshal([]byte(o.Actions), &actions)
	if err != nil {
		return actions, err
	}
	return actions, nil
}

type ResourceActions struct {
	ActionDiscoveryLua string                     `json:"discovery.lua,omitempty" yaml:"discovery.lua,omitempty" protobuf:"bytes,1,opt,name=actionDiscoveryLua"`
	Definitions        []ResourceActionDefinition `json:"definitions,omitempty" protobuf:"bytes,2,rep,name=definitions"`
}

type ResourceActionDefinition struct {
	Name      string `json:"name" protobuf:"bytes,1,opt,name=name"`
	ActionLua string `json:"action.lua" yaml:"action.lua" protobuf:"bytes,2,opt,name=actionLua"`
}
