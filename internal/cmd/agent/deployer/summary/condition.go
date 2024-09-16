package summary

import (
	"encoding/json"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
)

func getRawConditions(obj data.Object) []data.Object {
	statusAnn := obj.String("metadata", "annotations", "cattle.io/status")
	if statusAnn != "" {
		status := data.Object{}
		if err := json.Unmarshal([]byte(statusAnn), &status); err == nil {
			return append(obj.Slice("status", "conditions"), status.Slice("conditions")...)
		}
	}
	return obj.Slice("status", "conditions")
}

func getConditions(obj data.Object) (result []Condition) {
	for _, condition := range getRawConditions(obj) {
		result = append(result, Condition{Object: condition})
	}
	return
}

type Condition struct {
	data.Object
}

func (c Condition) Type() string {
	return c.String("type")
}

func (c Condition) Status() string {
	return c.String("status")
}

func (c Condition) Reason() string {
	return c.String("reason")
}

func (c Condition) Message() string {
	return c.String("message")
}

func (c Condition) Equals(other Condition) bool {
	return c.Type() == other.Type() &&
		c.Status() == other.Status() &&
		c.Reason() == other.Reason() &&
		c.Message() == other.Message()
}
