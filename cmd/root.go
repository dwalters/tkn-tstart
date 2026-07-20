package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dwalters/tkn-tstart/internal/envsubst"
	"github.com/dwalters/tkn-tstart/internal/run"
	"github.com/dwalters/tkn-tstart/internal/source"
	"github.com/dwalters/tkn-tstart/internal/tui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	namespace string
	dryRun    bool
	yes       bool
	showLog   bool
)

var Root = &cobra.Command{
	Use:   "tkn tstart [flags] FILE|URL",
	Short: "Start a Tekton TaskRun or PipelineRun with TUI parameter review",
	Long: `tkn tstart starts a Tekton TaskRun or PipelineRun from a YAML manifest.

The manifest may be a local file path or a URL:

  Local file           ./run.yaml  or  /path/to/run.yaml
  Generic HTTPS        https://example.com/run.yaml
  GitHub blob          https://github.com/user/repo/blob/main/run.yaml
  GitHub raw           https://raw.githubusercontent.com/user/repo/main/run.yaml
  GitHub Gist          https://gist.github.com/user/<id>          (first YAML file)
                       https://gist.github.com/user/<id>/raw/<rev>/<file>
  GitHub Enterprise    https://<host>/user/repo/blob/branch/run.yaml

GitHub and GHE credentials are resolved in order:
  1. Token stored by the gh CLI (gh auth login / GH_TOKEN / GITHUB_TOKEN)
  2. No authentication (public content)

Parameter values are resolved via bash-style environment variable substitution
before being presented in an interactive TUI for review and editing. Any
parameter whose value is a substitution expression that resolves to empty is
highlighted as required. Parameters explicitly set to "" in the manifest are
treated as intentionally empty and are not required.

Environment variable substitution reference:

  ${VAR}           Value of VAR; empty string if unset
  $VAR             Same as ${VAR}, no braces
  ${VAR:-default}  Value of VAR if set and non-empty, else "default"
  ${VAR-default}   Value of VAR if set (even if empty), else "default"
  ${VAR:=default}  Like :- but also assigns VAR=default in the environment
  ${VAR=default}   Like - but also assigns VAR=default in the environment
  ${VAR:+alt}      "alt" if VAR is set and non-empty, else empty string
  ${VAR+alt}       "alt" if VAR is set (even if empty), else empty string
  ${VAR:?message}  Error (exit 1) if VAR is unset or empty
  ${VAR?message}   Error (exit 1) if VAR is unset
  ${#VAR}          Length of VAR's value as a decimal string
  ${VAR#pattern}   Remove shortest prefix matching glob pattern from VAR
  ${VAR##pattern}  Remove longest prefix matching glob pattern from VAR
  ${VAR%pattern}   Remove shortest suffix matching glob pattern from VAR
  ${VAR%%pattern}  Remove longest suffix matching glob pattern from VAR

With --yes, substitution errors from :? and ? are fatal. In TUI mode they are
shown as empty values that the user can fill in.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return execute(args[0])
	},
}

func init() {
	Root.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (defaults to current context namespace)")
	Root.Flags().BoolVar(&dryRun, "dry-run", false, "Print the rendered manifest without applying it")
	Root.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the TUI and use environment variables only; error if any required parameter is unset")
	Root.Flags().BoolVar(&showLog, "showlog", false, "Stream run logs after submission (requires tkn in PATH)")
}

func execute(file string) error {
	data, err := source.Fetch(file)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	manifest, err := run.LoadBytes(data)
	if err != nil {
		return err
	}

	if err := applyEnvsubst(manifest, yes); err != nil {
		return err
	}

	if yes {
		if err := validateRequired(manifest.Params); err != nil {
			return err
		}
	} else {
		result, err := tui.Run(manifest)
		if err != nil {
			return fmt.Errorf("TUI: %w", err)
		}
		if !result.Submitted {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			os.Exit(1)
		}
		manifest.Params = result.Params
		if err := validateRequired(manifest.Params); err != nil {
			return err
		}
	}

	rendered, err := manifest.ApplyParams()
	if err != nil {
		return fmt.Errorf("rendering manifest: %w", err)
	}

	if dryRun {
		fmt.Print(string(rendered))
		return nil
	}

	if err := applyManifest(rendered, namespace); err != nil {
		return err
	}

	if showLog {
		name, err := extractName(rendered)
		if err != nil {
			return fmt.Errorf("--showlog: %w", err)
		}
		return followLogs(manifest.Kind, name, namespace)
	}

	return nil
}

// applyEnvsubst expands all param RawValues and stores the result in Value.
// In strict mode (--yes), :? and ? expressions that fail are returned as errors.
func applyEnvsubst(manifest *run.Manifest, strict bool) error {
	for _, p := range manifest.Params {
		if strict {
			v, err := envsubst.ExpandStrict(p.RawValue)
			if err != nil {
				return fmt.Errorf("param %q: %w", p.Name, err)
			}
			p.Value = v
		} else {
			p.Value = envsubst.Expand(p.RawValue)
		}

		// Apply the same expansion to the schema default.
		if p.Default != nil {
			expanded := envsubst.Expand(*p.Default)
			p.Default = &expanded
		}
	}
	return nil
}

func validateRequired(params []*run.Param) error {
	var missing []string
	for _, p := range params {
		if p.IsRequired() {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required parameters: %s", strings.Join(missing, ", "))
	}
	return nil
}

func applyManifest(data []byte, ns string) error {
	// generateName requires `create`; a fixed `name` can use `apply`.
	verb := kubectlVerb(data)
	args := []string{verb, "-f", "-"}
	if ns != "" {
		args = append(args, "--namespace", ns)
	}
	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %s: %w", verb, err)
	}
	return nil
}

// kubectlVerb returns "create" when the manifest uses generateName (apply
// doesn't support it), and "apply" otherwise.
func kubectlVerb(data []byte) string {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "apply"
	}
	meta, _ := doc["metadata"].(map[string]interface{})
	if meta == nil {
		return "apply"
	}
	if gn, _ := meta["generateName"].(string); gn != "" {
		if name, _ := meta["name"].(string); name == "" {
			return "create"
		}
	}
	return "apply"
}

func extractName(data []byte) (string, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	meta, _ := doc["metadata"].(map[string]interface{})
	if meta == nil {
		return "", fmt.Errorf("no metadata block in manifest")
	}
	name, _ := meta["name"].(string)
	if name == "" {
		return "", fmt.Errorf("run uses generateName; resolve the name from kubectl output and use: tkn %s logs -f <name>",
			kindResource(doc["kind"]))
	}
	return name, nil
}

func followLogs(kind, name, ns string) error {
	args := []string{kindResource(kind), "logs", "--follow", name}
	if ns != "" {
		args = append(args, "--namespace", ns)
	}
	cmd := exec.Command("tkn", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func kindResource(kind interface{}) string {
	switch fmt.Sprintf("%v", kind) {
	case "TaskRun":
		return "taskrun"
	case "PipelineRun":
		return "pipelinerun"
	default:
		return "taskrun"
	}
}
