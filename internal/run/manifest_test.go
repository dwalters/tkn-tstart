package run

import (
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

func mustLoad(t *testing.T, yaml string) *Manifest {
	t.Helper()
	m, err := LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return m
}

func findParam(m *Manifest, name string) *Param {
	for _, p := range m.Params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// ── LoadBytes ─────────────────────────────────────────────────────────────────

func TestLoadBytes_TaskRun(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: my-run
spec:
  params:
    - name: image
      value: docker.io/myimage:latest
  taskSpec:
    params:
      - name: image
        description: Container image
        type: string
`)
	if m.Kind != "TaskRun" {
		t.Errorf("Kind = %q", m.Kind)
	}
	p := findParam(m, "image")
	if p == nil {
		t.Fatal("param 'image' not found")
	}
	if p.Value != "docker.io/myimage:latest" {
		t.Errorf("Value = %q", p.Value)
	}
	if p.Description != "Container image" {
		t.Errorf("Description = %q", p.Description)
	}
	if !p.ExplicitInSpec {
		t.Error("ExplicitInSpec should be true")
	}
}

func TestLoadBytes_PipelineRun(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  generateName: my-pipeline-
spec:
  params:
    - name: cluster-name
      value: ${CLUSTER_NAME}
    - name: region
      value: ""
  pipelineSpec:
    params:
      - name: cluster-name
        type: string
      - name: region
        type: string
        default: us-east-1
`)
	if m.Kind != "PipelineRun" {
		t.Errorf("Kind = %q", m.Kind)
	}
	cn := findParam(m, "cluster-name")
	if cn == nil {
		t.Fatal("param 'cluster-name' not found")
	}
	if cn.RawValue != "${CLUSTER_NAME}" {
		t.Errorf("RawValue = %q", cn.RawValue)
	}

	r := findParam(m, "region")
	if r == nil {
		t.Fatal("param 'region' not found")
	}
	if r.RawValue != "" {
		t.Errorf("RawValue for explicitly-empty param should be empty, got %q", r.RawValue)
	}
}

func TestLoadBytes_UnsupportedKind(t *testing.T) {
	_, err := LoadBytes([]byte(`
kind: Task
metadata:
  name: foo
`))
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
	if !strings.Contains(err.Error(), "Task") {
		t.Errorf("error should mention the bad kind: %v", err)
	}
}

func TestLoadBytes_BadYAML(t *testing.T) {
	_, err := LoadBytes([]byte("{{{{not yaml"))
	if err == nil {
		t.Fatal("expected error for bad YAML")
	}
}

func TestLoadBytes_EnumParam(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: enum-run
spec:
  params:
    - name: env
      value: staging
  taskSpec:
    params:
      - name: env
        type: string
        enum:
          - dev
          - staging
          - prod
`)
	p := findParam(m, "env")
	if p == nil {
		t.Fatal("param 'env' not found")
	}
	if len(p.Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d: %v", len(p.Enum), p.Enum)
	}
	if p.Value != "staging" {
		t.Errorf("Value = %q", p.Value)
	}
}

func TestLoadBytes_SchemaDefault(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: defaults-run
spec:
  taskSpec:
    params:
      - name: retries
        type: string
        default: "3"
`)
	p := findParam(m, "retries")
	if p == nil {
		t.Fatal("param 'retries' not found")
	}
	if p.ExplicitInSpec {
		t.Error("ExplicitInSpec should be false for schema-only param")
	}
	if p.Default == nil || *p.Default != "3" {
		t.Errorf("Default = %v", p.Default)
	}
	if p.Value != "3" {
		t.Errorf("Value should be pre-filled from default, got %q", p.Value)
	}
}

func TestLoadBytes_NoSchema_FallsBackToSpecValues(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: no-schema-run
spec:
  params:
    - name: foo
      value: bar
`)
	p := findParam(m, "foo")
	if p == nil {
		t.Fatal("param 'foo' not found")
	}
	if p.Value != "bar" {
		t.Errorf("Value = %q", p.Value)
	}
}

// ── IsRequired ────────────────────────────────────────────────────────────────

func TestIsRequired(t *testing.T) {
	cases := []struct {
		name  string
		param Param
		want  bool
	}{
		{
			name:  "non-empty value is never required",
			param: Param{Value: "hello", ExplicitInSpec: true, RawValue: "hello"},
			want:  false,
		},
		{
			name:  "explicit empty string in spec is not required",
			param: Param{Value: "", ExplicitInSpec: true, RawValue: ""},
			want:  false,
		},
		{
			name:  "substitution that resolved to empty is required",
			param: Param{Value: "", ExplicitInSpec: true, RawValue: "${UNSET_VAR}"},
			want:  true,
		},
		{
			name:  "non-empty substitution result is not required",
			param: Param{Value: "resolved", ExplicitInSpec: true, RawValue: "${MY_VAR}"},
			want:  false,
		},
		{
			name:  "schema-only param with no default is required",
			param: Param{Value: "", ExplicitInSpec: false, Default: nil},
			want:  true,
		},
		{
			name:  "schema-only param with default (even empty) is not required",
			param: Param{Value: "", ExplicitInSpec: false, Default: strPtr("")},
			want:  false,
		},
		{
			name:  "schema-only param with non-empty default is not required",
			param: Param{Value: "v1.0", ExplicitInSpec: false, Default: strPtr("v1.0")},
			want:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.param.IsRequired()
			if got != c.want {
				t.Errorf("IsRequired() = %v, want %v", got, c.want)
			}
		})
	}
}

// ── ApplyParams ───────────────────────────────────────────────────────────────

func TestApplyParams(t *testing.T) {
	m := mustLoad(t, `
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: apply-test
spec:
  params:
    - name: foo
      value: original
  taskSpec:
    params:
      - name: foo
        type: string
`)
	// Simulate envsubst result.
	findParam(m, "foo").Value = "updated"

	out, err := m.ApplyParams()
	if err != nil {
		t.Fatalf("ApplyParams: %v", err)
	}
	if !strings.Contains(string(out), "updated") {
		t.Errorf("output should contain updated value:\n%s", out)
	}
	if strings.Contains(string(out), "original") {
		t.Errorf("output should not contain original value:\n%s", out)
	}
}
