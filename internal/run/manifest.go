package run

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Param represents one Tekton pipeline/task parameter and its resolved state.
type Param struct {
	Name           string
	Description    string
	Default        *string  // raw schema default (before envsubst), nil if not declared
	Enum           []string // valid values, if constrained
	RawValue       string   // value as it appears in spec.params before envsubst
	Value          string   // resolved value (set by caller after envsubst)
	ExplicitInSpec bool     // true if this param appears in spec.params (even with "")
}

// IsRequired returns true if the param needs a value from the user.
// A param is required when its resolved Value is empty AND it wasn't explicitly
// set to "" in spec.params. Params with an explicit "" are intentionally empty.
func (p *Param) IsRequired() bool {
	if p.Value != "" {
		return false
	}
	// Schema-only param with no default → required.
	if !p.ExplicitInSpec && p.Default == nil {
		return true
	}
	// Explicitly set to "" in spec.params → intentionally empty, not required.
	if p.ExplicitInSpec && p.RawValue == "" {
		return false
	}
	// Had a substitution expression that resolved to "" → user must supply a value.
	if p.ExplicitInSpec && p.RawValue != "" {
		return true
	}
	// Has a schema default (even if it expands to "") → not required.
	return false
}

// Manifest holds the parsed run document.
type Manifest struct {
	Kind   string // TaskRun or PipelineRun
	Raw    map[string]interface{}
	Params []*Param
}

// LoadBytes parses a manifest from YAML bytes.
func LoadBytes(data []byte) (*Manifest, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return parseDoc(doc)
}

// Load reads a local file and parses it as a manifest.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	return LoadBytes(data)
}

func parseDoc(doc map[string]interface{}) (*Manifest, error) {

	kind, _ := doc["kind"].(string)
	if kind != "TaskRun" && kind != "PipelineRun" {
		return nil, fmt.Errorf("unsupported kind %q: must be TaskRun or PipelineRun", kind)
	}

	params, err := extractParams(doc)
	if err != nil {
		return nil, err
	}

	return &Manifest{Kind: kind, Raw: doc, Params: params}, nil
}

func extractParams(doc map[string]interface{}) ([]*Param, error) {
	spec, _ := dig(doc, "spec").(map[string]interface{})
	if spec == nil {
		return nil, nil
	}

	schema := paramSchema(spec)
	specValues := specParamValues(spec)

	var params []*Param
	for _, s := range schema {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		p := &Param{
			Name:        strVal(sm, "name"),
			Description: strVal(sm, "description"),
		}

		if ev, ok := sm["enum"]; ok {
			if items, ok := ev.([]interface{}); ok {
				for _, item := range items {
					if sv, ok := item.(string); ok {
						p.Enum = append(p.Enum, sv)
					}
				}
			}
		}

		if dv, ok := sm["default"]; ok {
			sv := fmt.Sprintf("%v", dv)
			p.Default = &sv
		}

		if raw, inSpec := specValues[p.Name]; inSpec {
			p.ExplicitInSpec = true
			p.RawValue = raw
			p.Value = raw
		} else if p.Default != nil {
			p.RawValue = *p.Default
			p.Value = *p.Default
		}

		params = append(params, p)
	}

	// No schema: fall back to spec.params values only.
	if len(params) == 0 {
		for name, raw := range specValues {
			r := raw
			params = append(params, &Param{
				Name:           name,
				RawValue:       r,
				Value:          r,
				ExplicitInSpec: true,
				Default:        &r,
			})
		}
	}

	return params, nil
}

func paramSchema(spec map[string]interface{}) []interface{} {
	for _, key := range []string{"taskSpec", "pipelineSpec"} {
		if sub, ok := dig(spec, key).(map[string]interface{}); ok {
			if ps, ok := sub["params"].([]interface{}); ok {
				return ps
			}
		}
	}
	return nil
}

// specParamValues returns the raw (pre-expansion) string values from spec.params.
func specParamValues(spec map[string]interface{}) map[string]string {
	out := map[string]string{}
	raw, _ := spec["params"].([]interface{})
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := strVal(m, "name")
		val := ""
		if v, ok := m["value"]; ok {
			val = fmt.Sprintf("%v", v)
		}
		if name != "" {
			out[name] = val
		}
	}
	return out
}

func dig(m map[string]interface{}, key string) interface{} {
	if m == nil {
		return nil
	}
	return m[key]
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// ApplyParams writes the resolved param values back into Raw and returns YAML bytes.
func (m *Manifest) ApplyParams() ([]byte, error) {
	spec, _ := m.Raw["spec"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
		m.Raw["spec"] = spec
	}

	var paramList []interface{}
	for _, p := range m.Params {
		paramList = append(paramList, map[string]interface{}{
			"name":  p.Name,
			"value": p.Value,
		})
	}
	spec["params"] = paramList

	return yaml.Marshal(m.Raw)
}
