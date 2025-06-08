package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoList struct {
	Items []TodoItem `json:"items"`
}

// todo_read ---------------------------------------------------------------------------------------

type todoReadToolResult struct {
	Error string     `json:"error,omitempty"`
	Items []TodoItem `json:"items,omitempty"`
}

func (r todoReadToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var _ llm.Tool = (*todoReadTool)(nil)

type todoReadTool struct {
	logger logger.Logger
}

func NewTodoRead() *todoReadTool {
	return &todoReadTool{logger.NoOp()}
}

func (t *todoReadTool) SetLogger(logger logger.Logger) *todoReadTool {
	t.logger = logger
	return t
}

//go:embed todo_read.md
var todoReadToolDescription string

func (t *todoReadTool) Spec() (string, string, json.RawMessage) {
	return "todo_read", strings.TrimSpace(todoReadToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (t *todoReadTool) Call(ctx context.Context, _ string) (string, error) {
	todoList := loadTodoList()
	t.logger.Debugf("todo_read operation succeeded: found %d items", len(todoList.Items))
	return todoReadToolResult{Items: todoList.Items}.result()
}

// todo_write --------------------------------------------------------------------------------------

type todoWriteToolResult struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (r todoWriteToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var _ llm.Tool = (*todoWriteTool)(nil)

type todoWriteTool struct {
	logger logger.Logger
}

func NewTodoWrite() *todoWriteTool {
	return &todoWriteTool{logger.NoOp()}
}

func (t *todoWriteTool) SetLogger(logger logger.Logger) *todoWriteTool {
	t.logger = logger
	return t
}

//go:embed todo_write.md
var todoWriteToolDescription string

func (t *todoWriteTool) Spec() (string, string, json.RawMessage) {
	return "todo_write", strings.TrimSpace(todoWriteToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"todos": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "A unique ID. Incrementing numbers ('1', '2', ...) are preferred."
						},
						"content": {
							"type": "string",
							"minLength": 1
						},
						"status": {
							"type": "string",
							"enum": ["pending", "in_progress", "completed", "cancelled"]
						}
					},
					"required": ["id", "content", "status"]
				},
				"description": "The updated todo list"
			}
		},
		"required": ["todos"]
	}`)
}

func (t *todoWriteTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Errorf("todo_write tool called with invalid JSON arguments")
		return todoWriteToolResult{Error: "invalid JSON arguments"}.result()
	}
	todosData := gjson.Get(args, "todos")
	if !todosData.Exists() {
		t.logger.Errorf("todo_write operation failed: todos parameter is required")
		return todoWriteToolResult{Error: "todos parameter is required"}.result()
	}
	var items []TodoItem
	if err := json.Unmarshal([]byte(todosData.Raw), &items); err != nil {
		t.logger.Errorf("todo_write operation failed: invalid todos format: %s", err.Error())
		return todoWriteToolResult{Error: fmt.Sprintf("invalid todos format: %s", err.Error())}.result()
	}
	// validate all items have required fields and valid status
	for i, item := range items {
		if item.ID == "" {
			t.logger.Errorf("todo_write operation failed: item %d missing id", i)
			return todoWriteToolResult{Error: fmt.Sprintf("item %d missing id", i)}.result()
		}
		if item.Content == "" {
			t.logger.Errorf("todo_write operation failed: item %d missing content", i)
			return todoWriteToolResult{Error: fmt.Sprintf("item %d missing content", i)}.result()
		}
		if !isValidStatus(item.Status) {
			t.logger.Errorf("todo_write operation failed: item %d has invalid status: %s", i, item.Status)
			return todoWriteToolResult{Error: fmt.Sprintf("item %d has invalid status: %s", i, item.Status)}.result()
		}
	}
	// filter out cancelled items (they should be deleted)
	var activeItems []TodoItem
	for _, item := range items {
		if item.Status != "cancelled" {
			activeItems = append(activeItems, item)
		}
	}
	// store the active todo items (cancelled items are effectively deleted)
	saveTodoList(TodoList{Items: activeItems})
	// log the success
	t.logger.Debugf("todo_write operation succeeded: saved %d items (filtered out cancelled items)", len(activeItems))
	return todoWriteToolResult{Ok: true}.result()
}

// helpers -----------------------------------------------------------------------------------------

var (
	todoList   TodoList
	todoListMu sync.Mutex
)

func loadTodoList() TodoList {
	todoListMu.Lock()
	defer todoListMu.Unlock()
	return todoList
}

func saveTodoList(newTodoList TodoList) {
	todoListMu.Lock()
	defer todoListMu.Unlock()
	todoList = newTodoList
}

func isValidStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "cancelled":
		return true
	default:
		return false
	}
}
