package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
)

var _ llm.Tool = (*thinkTool)(nil)

type thinkTool struct {
	logger logger.Logger
}

func NewThink() *thinkTool {
	return &thinkTool{logger.NoOp()}
}

func (t *thinkTool) SetLogger(logger logger.Logger) *thinkTool {
	t.logger = logger
	return t
}

//go:embed think.md
var thinkToolDescription string

func (t *thinkTool) Spec() (string, string, json.RawMessage) {
	return "think", strings.TrimSpace(thinkToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"thought": {
				"type": "string",
				"description": "Your thought"
			}
		},
		"required": ["thought"]
	}`)
}

func (t *thinkTool) Call(ctx context.Context, args string) (string, error) {
	return "Thought logged", nil
}
