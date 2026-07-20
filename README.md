# tkn-tstart

A `tkn` plugin for starting Tekton `TaskRun` and `PipelineRun` resources with an interactive parameter review UI.

Drop the binary in your `PATH` as `tkn-tstart` and it becomes available as `tkn tstart`.

## Features

- **Interactive TUI** — review and edit all parameters before submitting, with inline descriptions and left/right cycling for enum params
- **Bash-style envsubst** — parameter values containing `${VAR}` expressions are resolved from the environment before the TUI opens (drone/flux compatible)
- **Smart required detection** — parameters set to `""` in the manifest are treated as intentionally empty; parameters whose substitution expression resolves to empty are flagged as required
- **Remote manifests** — load templates directly from GitHub, GitHub Enterprise, or any HTTPS URL without downloading first
- **Automated mode** — skip the TUI with `-y` for use in scripts and CI pipelines
- **Dry run** — render the substituted manifest without applying it
- **Log tailing** — pass `--showlog` to stream run logs immediately after submission

## Installation

```sh
go install github.com/dwalters/tkn-tstart@latest
```

The binary must be named `tkn-tstart` and on your `PATH`. After that:

```sh
tkn tstart --help
```

## Usage

```
tkn tstart [flags] FILE|URL
```

### Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--dry-run` | | Print the rendered manifest, do not apply |
| `--yes` | `-y` | Skip the TUI; error if any required param is unset |
| `--showlog` | | Stream run logs after submission (requires `tkn` in PATH) |
| `--namespace` | `-n` | Kubernetes namespace (default: current context) |

### Examples

```sh
# Open the TUI to review params, then start
tkn tstart run.yaml

# Load a template directly from GitHub
tkn tstart https://github.com/myorg/pipelines/blob/main/provision.yaml

# Load from a GitHub Gist (picks the first YAML file)
tkn tstart https://gist.github.com/alice/abc123def456

# Populate everything from env vars, skip the TUI
CLUSTER_NAME=dev-01 tkn tstart --yes provision.yaml

# Render the substituted YAML without applying
CLUSTER_NAME=dev-01 tkn tstart --dry-run --yes provision.yaml

# Start and immediately tail logs
tkn tstart --showlog run.yaml
```

## The TUI

```
PipelineRun  provision-fyre-(generated)

cluster-name                    █
                                cluster name to provision
cluster-url                     (empty)
                                for an existing cluster, url of the cluster api
cluster-admin-username          kubeadmin
ocp-version                     4.22.2
target-env                      [ dev ]  [ staging ]  [ prod ]
                                target deployment environment
...

tab/↑↓ navigate  ←/→ select enum  enter/ctrl+s start  esc cancel
```

- Parameters whose value resolved to empty from a `${VAR}` expression are marked with `*` and block submission until filled
- Parameters explicitly set to `""` in the manifest are shown as `(empty)` and are not required
- Enum parameters show a horizontal picker; navigate with `←` / `→`

## Manifest format

`tkn-tstart` accepts standard Tekton `TaskRun` and `PipelineRun` YAML. Parameter schema (descriptions, defaults, enums) is read from the inline `taskSpec`/`pipelineSpec` block.

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  generateName: provision-fyre-
spec:
  params:
    - name: cluster-name
      value: ${CLUSTER_NAME}          # required — must be provided
    - name: ocp-version
      value: "4.22.2"                 # pre-filled, editable in TUI
    - name: cluster-url
      value: ""                       # intentionally empty, not flagged as required
  pipelineSpec:
    params:
      - name: cluster-name
        description: cluster name to provision
        type: string
      - name: ocp-version
        type: string
        default: "4.16.0"
      - name: target-env
        type: string
        enum: [dev, staging, prod]
```

### generateName

Manifests that use `generateName` (without a fixed `name`) are submitted with `kubectl create` instead of `kubectl apply`, since `apply` requires a stable name.

## Environment variable substitution

All parameter values are expanded before the TUI opens. Supported forms:

| Expression | Result |
|------------|--------|
| `${VAR}` | Value of `VAR`; empty string if unset |
| `$VAR` | Same, without braces |
| `${VAR:-default}` | Value of `VAR` if set and non-empty, else `default` |
| `${VAR-default}` | Value of `VAR` if set (even if empty), else `default` |
| `${VAR:=default}` | Like `:-` but also assigns `VAR=default` in the environment |
| `${VAR=default}` | Like `-` but also assigns `VAR=default` in the environment |
| `${VAR:+alt}` | `alt` if `VAR` is set and non-empty, else empty |
| `${VAR+alt}` | `alt` if `VAR` is set (even if empty), else empty |
| `${VAR:?message}` | Error (exit 1 with `--yes`) if `VAR` is unset or empty |
| `${VAR?message}` | Error (exit 1 with `--yes`) if `VAR` is unset |
| `${#VAR}` | Length of `VAR`'s value |
| `${VAR#pattern}` | Remove shortest prefix matching glob `pattern` |
| `${VAR##pattern}` | Remove longest prefix matching glob `pattern` |
| `${VAR%pattern}` | Remove shortest suffix matching glob `pattern` |
| `${VAR%%pattern}` | Remove longest suffix matching glob `pattern` |

In TUI mode, `:?` and `?` expressions that fail produce an empty value that can be filled in interactively. With `--yes` they are fatal errors.

## Remote manifests

The `FILE|URL` argument accepts:

| Source | Example |
|--------|---------|
| Local path | `./run.yaml` |
| Generic HTTPS | `https://example.com/run.yaml` |
| GitHub blob URL | `https://github.com/org/repo/blob/main/run.yaml` |
| GitHub raw URL | `https://raw.githubusercontent.com/org/repo/main/run.yaml` |
| GitHub Gist | `https://gist.github.com/user/<id>` |
| GitHub Gist (specific file) | `https://gist.github.com/user/<id>/raw/<rev>/<file>` |
| GitHub Enterprise | `https://github.example.com/org/repo/blob/main/run.yaml` |

### Authentication

Credentials are resolved automatically in this order:

1. Token stored by the [`gh` CLI](https://cli.github.com/) (`gh auth login`)
2. `GH_TOKEN` / `GITHUB_TOKEN` environment variables
3. No authentication (public content only)

For GitHub Enterprise, run `gh auth login --hostname your.ghe.host` once and credentials are picked up automatically.

When a Gist URL points to a page with multiple files, the first `.yaml` / `.yml` file is used. If none exist, the first file of any type is used.

## Requirements

- `kubectl` in PATH (for applying manifests)
- `tkn` in PATH (only for `--showlog`)
- `gh` in PATH (optional, for authenticated GitHub access)
