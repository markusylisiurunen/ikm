package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
)

type thinkToolResult struct {
	Result string `json:"result"`
}

func (r thinkToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var thinkToolDescription = strings.TrimSpace(`
Use this tool to think through complex problems or brainstorm solutions step-by-step. It will not obtain new information or make any changes to the file system, but just log the thought.

For example, use when you need to:
- Break down complex tasks and plan your approach
- Reason through multiple solutions and their trade-offs
- Reflect on what you've learned from exploring code
- Work through edge cases or potential issues
- Brainstorm creative solutions when stuck
- Assess the simplest and most effective changes

Example: After discovering a bug's source, use this to brainstorm fixes and evaluate which approach would be cleanest and most maintainable.

Be thorough, your reasoning helps you make better decisions.
`)

var _ llm.Tool = (*thinkTool)(nil)

type thinkTool struct {
	logger logger.Logger
}

func NewThink() *thinkTool {
	return &thinkTool{
		logger: logger.NoOp(),
	}
}

func (t *thinkTool) SetLogger(logger logger.Logger) *thinkTool {
	t.logger = logger
	return t
}

func (t *thinkTool) Spec() (string, string, json.RawMessage) {
	return "think", thinkToolDescription, json.RawMessage(`{
		"type": "object",
		"properties": {
			"thought": {
				"type": "string",
				"description": "Your thoughts."
			}
		},
		"required": ["thought"]
	}`)
}

func (t *thinkTool) Call(ctx context.Context, args string) (string, error) {
	return thinkToolResult{Result: "Thought logged."}.result()
}
