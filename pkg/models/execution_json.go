package models

import (
	"encoding/json"
	"fmt"
	"strings"
)

// UnmarshalJSON refuses removed execution keys before they can silently drop
// dispatch links when a program reads and rewrites a configuration.
func (c *ExecutionConfig) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, removed := range []string{"role", "workers"} {
		for key := range fields {
			if strings.EqualFold(key, removed) {
				return fmt.Errorf("execution.%s was removed; use execution.choreographers for linked nodes and dispat choreographer to serve tasks", removed)
			}
		}
	}
	type executionConfigJSON ExecutionConfig
	return json.Unmarshal(data, (*executionConfigJSON)(c))
}
