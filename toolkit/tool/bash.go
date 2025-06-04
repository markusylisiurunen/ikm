package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

const (
	bashToolMaxCmdLength = 8192
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

var _ llm.Tool = (*bashTool)(nil)

type bashTool struct {
	logger logger.Logger
	exec   func(context.Context, string) (int, string, string, error)
}

func NewBash(exec func(context.Context, string) (int, string, string, error)) *bashTool {
	return &bashTool{logger.NoOp(), exec}
}

func (t *bashTool) SetLogger(logger logger.Logger) *bashTool {
	t.logger = logger
	return t
}

//go:embed bash.md
var bashToolDescription string

func (t *bashTool) Spec() (string, string, json.RawMessage) {
	return "bash", strings.TrimSpace(bashToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The command to execute"
			}
		},
		"required": ["command"]
	}`)
}

func (t *bashTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Error("bash tool called with invalid JSON arguments")
		return bashToolResult{Ok: false, Error: "invalid JSON arguments"}.result()
	}
	cmd := gjson.Get(args, "command").String()
	if cmd == "" {
		t.logger.Error("bash tool called without command")
		return bashToolResult{Ok: false, Error: "command is required"}.result()
	}
	if len(cmd) > bashToolMaxCmdLength {
		t.logger.Error("bash tool called with command exceeding max length: %d", len(cmd))
		return bashToolResult{Ok: false, Error: fmt.Sprintf("command exceeds maximum length of %d characters", bashToolMaxCmdLength)}.result()
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
