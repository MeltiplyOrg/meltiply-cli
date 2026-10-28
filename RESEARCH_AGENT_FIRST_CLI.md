# Agent-First CLI Architecture & OpenAPI Integration Guide

Research findings from [CircleCI CLI](https://github.com/CircleCI-Public/circleci-cli) and [Cloudflare CLI (`cf`)](https://github.com/cloudflare/cf), paired with a concrete architecture blueprint for building an agent-first CLI in Go backed by an OpenAPI SaaS application.

This is a prospective architecture blueprint, not the implemented command
reference. See [README.md](./README.md) for the current `melti-cli` surface and
the required backend authorization work.

---

## 1. Executive Research Summary

### 1.1 CircleCI CLI (`circleci-cli`)
- **Technology**: Go (`spf13/cobra`, `clikit`, `iostream`, `bubbletea`).
- **Core Pattern**: Strict UNIX philosophy, structured errors, and machine-first stream discipline.
- **Key Discoveries**:
  1. **Structured Error Hierarchy (`clikit/errors`)**: Errors are strongly typed (`CLIError`) containing machine `code`, scannable `title`, detailed `message`, actionable `suggestions[]`, doc `ref`, and discrete `exit_code`.
  2. **Standardized Exit Codes**:
     - `0`: Success
     - `1`: General Error
     - `2`: Bad Arguments / Validation
     - `3`: Auth Error
     - `4`: API Error
     - `5`: Not Found
     - `6`: Cancelled / Interrupted
     - `7`: Validation Fail
     - `8`: Timeout
  3. **Stream Isolation (`clikit/iostream.Streams`)**:
     - `stdout`: Pure structured data payload.
     - `stderr`: Status messages, progress bars, logs, and structured JSON errors.
     - TTY detection independently checked on both stdout and stderr via `golang.org/x/term`.
     - Signal isolation: `signal.Ignore(syscall.SIGPIPE)` prevents SIGPIPE crashes when piped to `head -n 1`.
  4. **Embedded `jq` Engine**: Direct integration of `gojq` via `--jq <expr>`. Eliminates external host binary dependency, allowing caller agents to filter large responses inside the CLI process.
  5. **Agent-Aware Login**: `internal/cmd/cmdauth/login.go` detects if an AI agent is running the CLI; if true, it suppresses interactive TUI prompts and launches the system browser (`browser.OpenURL`) in the background while listening on a loopback callback server.
  6. **Update Check Suppression**: Automatically skips update checks and banner printouts when an agent environment is detected to keep stdout/stderr clean.

### 1.2 Cloudflare CLI (`cf`)
- **Technology**: TypeScript / ESM (`yargs`, Vite+ / Rolldown, Forge OpenAPI generator).
- **Core Pattern**: "The agentic CLI for the entire Cloudflare API".
- **Key Discoveries**:
  1. **MCP Tool Export (`cf tools`)**: Converts the internal command metadata catalog (`commands.json`) directly into Model Context Protocol (MCP) tool schemas. LLM clients and agents can dynamically discover and register all CLI operations.
  2. **API Schema Introspection (`cf schema <command>`)**: Dumps the exact JSON Schema for path params, query params, and request body fields for any endpoint.
  3. **Intent-Based Search (`cf cli search <query>`)**: Uses MiniSearch indexing across command paths, summaries, descriptions, and arguments. Agents search by intent (e.g. `"create dns record"`) rather than crawling `--help`.
  4. **Universal `--dry-run`**: Every command supports `--dry-run`, returning a structured JSON payload showing the HTTP method, resolved target URL, path parameters, query parameters, and serialized request body without triggering mutations.
  5. **Input Bypass (`--body <json|@file.json|->`)**: Avoids flag explosion and shell escaping bugs by allowing callers to provide the raw JSON body directly.
  6. **Automated Agent Detection (`lib/agent-context.ts`)**: Detects 12+ agent harnesses via process environment variables (`CLAUDECODE`, `CURSOR_AGENT`, `CODEX_THREAD_ID`, `PI_CODING_AGENT`, `OPENCODE`, `AI_AGENT`, etc.) and attaches telemetry/audit headers (`X-CF-CLI-Agent`).

---

## 2. The 7 Pillars of an Agent-First CLI

Traditional CLIs optimize for human typing; Agent-First CLIs optimize for machine reliability, context-window efficiency, and deterministic recovery.

```
+-----------------------------------------------------------------------------------+
|                              Agent-First CLI Pillars                              |
+-------------------+--------------------------------+------------------------------+
| 1. Discovery      | `mycli search <query>`         | Fast intent-based search     |
| 2. Introspection  | `mycli tools` & `mycli schema` | MCP tool spec & JSON schemas |
| 3. Output Stream  | Raw JSON default for agents    | Zero ANSI, stderr for logs   |
| 4. Context Saving | Built-in `--jq <filter>`       | Filters payload in Go memory |
| 5. Safety First   | Universal `--dry-run`          | Verifies params without side |
|                   |                                | effects                      |
| 6. Flexible Input | `--body <json|@file|->`        | Raw OpenAPI payload bypass   |
| 7. Self-Healing   | Structured `suggestions[]`     | Deterministic error recovery |
+-------------------+--------------------------------+------------------------------+
```

---

## 3. Recommended Go Project Layout

```
melti-cli/
├── cmd/
│   └── melti-cli/
│       └── main.go                 # Process entry: signal handling & exit codes
├── pkg/
│   ├── agent/                      # Agent harness detector
│   │   ├── detect.go
│   │   └── detect_test.go
│   ├── apiclient/                  # Generated Go HTTP client & OpenAPI types
│   │   └── client.gen.go           # Generated via oapi-codegen
│   ├── clierror/                   # Structured CLIError and standardized exit codes
│   │   └── error.go
│   ├── config/                     # Profile, token, and endpoint resolution
│   │   └── config.go
│   ├── iostream/                   # Streams, TTY detection, embedded gojq
│   │   └── streams.go
│   ├── openapi/                    # Embedded OpenAPI spec & runtime inspection
│   │   ├── spec.go                 # //go:embed openapi.json
│   │   ├── schema.go               # Schema lookups for commands
│   │   └── search.go               # In-memory keyword/intent search
│   └── commands/
│       ├── root.go                 # Root Cobra command & global flags
│       ├── auth/                   # login, logout, whoami
│       ├── schema/                 # `melti-cli schema <resource> <action>`
│       ├── tools/                  # `melti-cli tools` (MCP tool export)
│       ├── search/                 # `melti-cli search <query>`
│       └── _generated/             # Auto-generated resource commands
├── spec/
│   └── openapi.json                # Upstream SaaS OpenAPI spec
├── tools/
│   └── cmdgen/                     # Code generator: OpenAPI -> Cobra commands
│       └── main.go
├── Makefile                        # Generation, testing, and build targets
├── .gitignore
├── go.mod
└── go.sum
```

---

## 4. Production Go Reference Implementations

### 4.1 Agent Harness Detector (`pkg/agent/detect.go`)

```go
package agent

import (
	"os"
	"strings"
)

type Harness struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
}

type Context struct {
	IsAgent bool     `json:"is_agent"`
	Harness *Harness `json:"harness,omitempty"`
}

func Detect() Context {
	if val := os.Getenv("AI_AGENT"); val != "" {
		return Context{
			IsAgent: true,
			Harness: &Harness{ID: strings.TrimSpace(val)},
		}
	}

	detectors := []struct {
		envKey    string
		expected  string
		harnessID string
		sessionEV string
	}{
		{"CLAUDECODE", "1", "claude-code", "CLAUDE_CODE_SESSION_ID"},
		{"CURSOR_AGENT", "1", "cursor-agent", "CURSOR_CONVERSATION_ID"},
		{"CODEX_SANDBOX", "", "codex", "CODEX_THREAD_ID"},
		{"PI_CODING_AGENT", "true", "pi", "PI_SESSION_ID"},
		{"OPENCODE", "1", "opencode", ""},
		{"GEMINI_CLI", "1", "gemini-cli", ""},
		{"CRUSH", "1", "crush", ""},
	}

	for _, d := range detectors {
		v, exists := os.LookupEnv(d.envKey)
		if !exists {
			continue
		}
		if d.expected == "" || v == d.expected {
			var sessionID string
			if d.sessionEV != "" {
				sessionID = os.Getenv(d.sessionEV)
			}
			return Context{
				IsAgent: true,
				Harness: &Harness{
					ID:        d.harnessID,
					SessionID: sessionID,
				},
			}
		}
	}

	return Context{IsAgent: false}
}
```

### 4.2 Structured Error Contract (`pkg/clierror/error.go`)

```go
package clierror

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ExitSuccess        = 0
	ExitGeneralError   = 1
	ExitBadArguments   = 2
	ExitAuthError      = 3
	ExitAPIError       = 4
	ExitNotFound       = 5
	ExitCancelled      = 6
	ExitValidationFail = 7
	ExitTimeout        = 8
)

type CLIError struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Suggestions []string `json:"suggestions,omitempty"`
	Ref         string   `json:"ref,omitempty"`
	ExitCode    int      `json:"exit_code"`
}

func (e *CLIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Title, e.Message)
}

func New(code, title, message string) *CLIError {
	return &CLIError{
		Code:     code,
		Title:    title,
		Message:  message,
		ExitCode: ExitGeneralError,
	}
}

func (e *CLIError) WithSuggestions(suggestions ...string) *CLIError {
	e.Suggestions = append(e.Suggestions, suggestions...)
	return e
}

func (e *CLIError) WithExitCode(code int) *CLIError {
	e.ExitCode = code
	return e
}

func (e *CLIError) WithRef(url string) *CLIError {
	e.Ref = url
	return e
}

func (e *CLIError) FormatJSON() string {
	payload := struct {
		Error bool `json:"error"`
		*CLIError
	}{
		Error:    true,
		CLIError: e,
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	return string(b) + "\n"
}

func (e *CLIError) FormatPlain() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "error [%s]: %s\n", e.Code, e.Message)
	if len(e.Suggestions) > 0 {
		b.WriteString("\nSuggestions:\n")
		for _, s := range e.Suggestions {
			_, _ = fmt.Fprintf(&b, "  • %s\n", s)
		}
	}
	if e.Ref != "" {
		_, _ = fmt.Fprintf(&b, "\nDocs: %s\n", e.Ref)
	}
	return b.String()
}
```

### 4.3 Stream Controller & Built-In JQ (`pkg/iostream/streams.go`)

```go
package iostream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/itchyny/gojq"
	"golang.org/x/term"
)

type Streams struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	IsTTY   bool
	IsAgent bool
	Quiet   bool
}

func New(isAgent bool) *Streams {
	isTTY := term.IsTerminal(int(os.Stdout.Fd())) && !isAgent
	return &Streams{
		In:      os.Stdin,
		Out:     os.Stdout,
		Err:     os.Stderr,
		IsTTY:   isTTY,
		IsAgent: isAgent,
	}
}

func (s *Streams) PrintJSON(v any, jqFilter string) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}

	if jqFilter != "" {
		query, err := gojq.Parse(jqFilter)
		if err != nil {
			return fmt.Errorf("invalid jq expression: %w", err)
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return err
		}
		iter := query.Run(parsed)
		for {
			val, ok := iter.Next()
			if !ok {
				break
			}
			if err, ok := val.(error); ok {
				return err
			}
			line, _ := json.Marshal(val)
			_, _ = s.Out.Write(append(line, '\n'))
		}
		return nil
	}

	if !s.IsTTY || s.IsAgent {
		_, _ = s.Out.Write(append(raw, '\n'))
		return nil
	}

	var formatted bytes.Buffer
	_ = json.Indent(&formatted, raw, "", "  ")
	_, _ = s.Out.Write(append(formatted.Bytes(), '\n'))
	return nil
}
```

### 4.4 MCP Tool Specification Generator (`pkg/commands/tools/tools.go`)

```go
package tools

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type MCPProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func NewCmdTools(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "tools",
		Hidden: true, // Internal/Agent use
		Short:  "Export all CLI operations as Model Context Protocol (MCP) tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			var tools []MCPTool

			var walk func(c *cobra.Command)
			walk = func(c *cobra.Command) {
				if c.Runnable() && !c.Hidden {
					name := strings.ReplaceAll(c.CommandPath(), " ", "_")
					props := map[string]MCPProperty{}
					var required []string

					c.Flags().VisitAll(func(f *pflag.Flag) {
						props[f.Name] = MCPProperty{
							Type:        f.Value.Type(),
							Description: f.Usage,
						}
						if f.Annotations != nil && len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0 {
							required = append(required, f.Name)
						}
					})

					tools = append(tools, MCPTool{
						Name:        name,
						Description: c.Short,
						InputSchema: map[string]interface{}{
							"type":       "object",
							"properties": props,
							"required":   required,
						},
					})
				}
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}

			walk(rootCmd)

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]interface{}{
				"version": "1.0",
				"tools":   tools,
			})
		},
	}
}
```

### 4.5 Mutation Safeguards (`--dry-run` and `--body`)

```go
package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"melti-cli/pkg/clierror"
	"melti-cli/pkg/iostream"
)

type MutationOpts struct {
	DryRun bool
	Body   string
}

func HandleDryRun(s *iostream.Streams, method, url string, body any) error {
	return s.PrintJSON(map[string]any{
		"dry_run": true,
		"method":  method,
		"url":     url,
		"body":    body,
	}, "")
}

func ResolveBody(bodyFlag string, fallback any) (any, error) {
	if bodyFlag == "" {
		return fallback, nil
	}

	var raw []byte
	var err error

	if bodyFlag == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else if strings.HasPrefix(bodyFlag, "@") {
		raw, err = os.ReadFile(strings.TrimPrefix(bodyFlag, "@"))
	} else {
		raw = []byte(bodyFlag)
	}

	if err != nil {
		return nil, clierror.New("input.read_failed", "Failed to read request body", err.Error()).
			WithExitCode(clierror.ExitBadArguments)
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, clierror.New("input.invalid_json", "Request body is not valid JSON", err.Error()).
			WithSuggestions("Check JSON syntax using jq or a linter").
			WithExitCode(clierror.ExitBadArguments)
	}

	return parsed, nil
}
```

---

## 5. Step-by-Step Implementation Strategy

1. **Bootstrap Go Module & Generator**:
   ```sh
   go mod init melti-cli
   go get github.com/spf13/cobra@latest
   go get github.com/spf13/pflag@latest
   go get github.com/itchyny/gojq@latest
   go get golang.org/x/term@latest
   go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
   ```

2. **Generate Client Bindings**:
   Place your SaaS OpenAPI spec in `spec/openapi.json`. Run `oapi-codegen` to produce typed request/response structs and an HTTP client in `pkg/apiclient`.

3. **Build Core Command Generator (`tools/cmdgen`)**:
   Parse OpenAPI path items and operations (`GET`, `POST`, `PUT`, `DELETE`). For each operation:
   - Generate a Cobra subcommand under `pkg/commands/_generated/`.
   - Register path and query parameters as flags.
   - For mutations (`POST`/`PUT`/`PATCH`/`DELETE`), attach `--dry-run` and `--body`.

4. **Add Agent Self-Discovery**:
   - Register `tools` command to dump MCP specs.
   - Register `schema` command to inspect embedded OpenAPI schemas.
   - Register `search` command using in-memory lexical matching.
