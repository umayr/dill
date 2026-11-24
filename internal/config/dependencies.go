package config

import (
	"encoding/json"
	"fmt"
)

// DependencyCondition mirrors the Pkl string-literal union.
type DependencyCondition string

const (
	Started   DependencyCondition = "started"
	Healthy   DependencyCondition = "healthy"
	Completed DependencyCondition = "completed"
)

type dependency struct {
	Service   string              `json:"service"`
	Condition DependencyCondition `json:"condition"`
}

// Normalize only the service dependsOn union before the existing strict decoder.
// Job dependencies and all unrelated fields keep their existing validation.
func decodeDependencies(raw json.RawMessage) (json.RawMessage, map[string]map[string]DependencyCondition, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	var services map[string]json.RawMessage
	if v := obj["services"]; v == nil {
		return raw, nil, nil
	} else if err := json.Unmarshal(v, &services); err != nil {
		return nil, nil, err
	}
	out := map[string]map[string]DependencyCondition{}
	for name, v := range services {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(v, &fields); err != nil {
			return nil, nil, err
		}
		deps := fields["dependsOn"]
		if deps == nil {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(deps, &items); err != nil {
			return nil, nil, err
		}
		names := make([]string, 0, len(items))
		conditions := map[string]DependencyCondition{}
		for _, item := range items {
			var s string
			if len(item) > 0 && item[0] == '"' {
				if err := json.Unmarshal(item, &s); err != nil {
					return nil, nil, err
				}
			} else {
				var d dependency
				if err := decodeStrict(item, &d); err != nil {
					return nil, nil, err
				}
				switch d.Condition {
				case Started, Healthy, Completed:
				default:
					return nil, nil, fmt.Errorf("invalid dependency condition for %q", name)
				}
				s = d.Service
				conditions[s] = d.Condition
			}
			names = append(names, s)
		}
		if len(conditions) != 0 {
			seen := map[string]bool{}
			for _, n := range names {
				if n == "" || seen[n] {
					return nil, nil, fmt.Errorf("empty or duplicate dependency for %q", name)
				}
				seen[n] = true
			}
			fields["dependsOn"], _ = json.Marshal(names)
			services[name], _ = json.Marshal(fields)
			out[name] = conditions
		}
	}
	if len(out) == 0 {
		return raw, nil, nil
	}
	obj["services"], _ = json.Marshal(services)
	b, err := json.Marshal(obj)
	return b, out, err
}

// MarshalJSON preserves conditions in the effective config shown to operators.
func (s Service) MarshalJSON() ([]byte, error) {
	type plain Service
	if len(s.Conditions) == 0 {
		return json.Marshal(plain(s))
	}
	items := make([]any, 0, len(s.DependsOn))
	for _, n := range s.DependsOn {
		if c, ok := s.Conditions[n]; ok {
			items = append(items, dependency{n, c})
		} else {
			items = append(items, n)
		}
	}
	return json.Marshal(struct {
		plain
		Depends []any `json:"dependsOn"`
	}{plain(s), items})
}
