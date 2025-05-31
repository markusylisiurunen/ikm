package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

type bashToolResult struct {
	Ok     bool   `json:"ok"`
	Error  string `json:"error,omitzero"`
	Stdout string `json:"stdout,omitzero"`
	Stderr string `json:"stderr,omitzero"`
}

func (r bashToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var bashToolDescription = strings.TrimSpace(`
Execute bash commands within a secure Ubuntu Noble sandbox environment, operating in the root directory of the current project.

- Constraints: The sandbox has a read-only filesystem (files cannot be modified) and network access is disabled.
- Permissions: You can run any command that only reads from the filesystem and does not require network access.
- Examples: ls, nl -ba -w1, rg, sed (for viewing), git status, git diff, git log.
- Prohibited: Commands attempting to modify files (sed -i, mv, rm, git commit) or access the network (curl, wget, git clone) will fail.
`)

var _ llm.Tool = (*bashTool)(nil)

type bashTool struct {
	logger logger.Logger
	exec   func(context.Context, string) (int, string, string, error)
}

func NewBash(exec func(context.Context, string) (int, string, string, error)) *bashTool {
	return &bashTool{
		logger: logger.NoOp(),
		exec:   exec,
	}
}

func (t *bashTool) SetLogger(logger logger.Logger) *bashTool {
	t.logger = logger
	return t
}

func (t *bashTool) Spec() (string, string, json.RawMessage) {
	return "bash", bashToolDescription, json.RawMessage(`{
		"type": "object",
		"properties": {
			"cmd": {
				"type": "string",
				"description": "Bash command to execute"
			}
		},
		"required": ["cmd"]
	}`)
}

func (t *bashTool) Call(ctx context.Context, args string) (string, error) {
	cmd := gjson.Get(args, "cmd").String()
	if cmd == "" {
		t.logger.Error("bash tool called without command")
		return fsToolResult{Ok: false, Error: "command is required"}.result()
	}
	_, stdout, stderr, err := t.exec(ctx, cmd)
	if err != nil {
		t.logger.Error("bash tool execution of %q failed: %s", cmd, err.Error())
		return bashToolResult{Ok: false, Error: err.Error()}.result()
	}
	t.logger.Debug("bash tool executed %q successfully", cmd)
	return bashToolResult{
		Ok:     true,
		Stdout: stdout,
		Stderr: stderr,
	}.result()
}
