package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

const (
	taskToolExecTimeout     = 5 * time.Minute
	taskToolMaxDescLength   = 8192
	taskToolMaxReportLength = 16384
	taskToolMaxTurns        = 32
	taskToolMaxUserPrompts  = 3
)

type taskToolResult struct {
	Error  string `json:"error,omitzero"`
	Report string `json:"report,omitzero"`
}

func (r taskToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var taskToolDescription = strings.TrimSpace(`
Execute an autonomous task using an AI agent with file system access. The task description should include all necessary execution and reporting instructions.

Effort levels:
- Fast: Uses a capable LLM model optimized for speed. Best for: file operations, simple code changes, data extraction, agentic search, basic analysis.
- Thorough: Uses the most advanced model available. Best for: complex reasoning, architectural decisions, multi-step workflows, research tasks.

Agent capabilities:
- File system operations (read, write, list, remove)
- 5-minute execution timeout
- Autonomous operation (no clarification requests)

<good_task_description_example>
Analyze main.go and create comprehensive test coverage. Follow these steps:
1. Read and understand the main.go file structure
2. Identify all exported functions and methods
3. Create test files with comprehensive coverage
4. Include edge cases and error scenarios
5. Use standard Go testing patterns

Report back in simple Markdown format with:
- Files created: List of new files created with descriptions
- Test coverage: Percentage estimate and key areas left uncovered
- Issues found: Any architectural concerns discovered
</good_task_description_example>
`)

var _ llm.Tool = (*taskTool)(nil)

type taskTool struct {
	logger                 logger.Logger
	openRouterToken        string
	fastButCapableModel    string
	thoroughButCostlyModel string
}

func NewTask(openRouterToken, fastButCapableModel, thoroughButCostlyModel string) *taskTool {
	return &taskTool{
		logger:                 logger.NoOp(),
		openRouterToken:        openRouterToken,
		fastButCapableModel:    fastButCapableModel,
		thoroughButCostlyModel: thoroughButCostlyModel,
	}
}

func (t *taskTool) SetLogger(logger logger.Logger) *taskTool {
	t.logger = logger
	return t
}

func (t *taskTool) Spec() (string, string, json.RawMessage) {
	return "task", taskToolDescription, json.RawMessage(`{
		"type": "object",
		"properties": {
			"effort": {
				"type": "string",
				"enum": ["fast", "thorough"],
				"description": "The desired effort level for the task"
			},
			"task": {
				"type": "string",
				"description": "A natural language description of the task to be performed, including any specific execution steps and reporting requirements"
			}
		},
		"required": ["effort", "task"]
	}`)
}

func (t *taskTool) Call(ctx context.Context, args string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, taskToolExecTimeout)
	defer cancel()
	if !gjson.Valid(args) {
		t.logger.Error("task tool called with invalid JSON arguments")
		return taskToolResult{Error: "invalid JSON arguments"}.result()
	}
	effort := gjson.Get(args, "effort").String()
	desc := gjson.Get(args, "task").String()
	if desc == "" {
		return taskToolResult{Error: "task description cannot be empty"}.result()
	}
	if len(desc) > taskToolMaxDescLength {
		return taskToolResult{Error: fmt.Sprintf("task description exceeds maximum length of %d characters", taskToolMaxDescLength)}.result()
	}
	var modelName string
	switch effort {
	case "fast":
		modelName = t.fastButCapableModel
	case "thorough":
		modelName = t.thoroughButCostlyModel
	default:
		return taskToolResult{Error: "effort must be 'fast' or 'thorough'"}.result()
	}
	if modelName == "" {
		return taskToolResult{Error: fmt.Sprintf("no model configured for effort level '%s'", effort)}.result()
	}
	t.logger.Debug("executing task with effort %q and model %q: %s", effort, modelName, desc)
	reporter := &writeReportTool{}
	model := llm.NewOpenRouter(t.logger, t.openRouterToken, modelName)
	model.Register(NewFS())
	model.Register(NewLLM(t.openRouterToken))
	model.Register(reporter)
	history := []llm.Message{
		t.systemMessage(),
		t.initialUserMessage(desc),
	}
	userPromptCount := 1
	for userPromptCount <= taskToolMaxUserPrompts {
		events := model.Stream(ctx, history,
			llm.WithMaxTokens(16384),
			llm.WithMaxTurns(taskToolMaxTurns),
			llm.WithTemperature(0.7),
			llm.WithStopCondition(func(_ int, _ []llm.Message) bool { return len(reporter.report) > 0 }),
		)
		messages, _, err := llm.Rollup(events)
		if err != nil {
			t.logger.Error("task execution failed: %s", err.Error())
			return taskToolResult{Error: fmt.Sprintf("task execution failed: %s", err.Error())}.result()
		}
		history = append(history, messages...)
		for _, msg := range messages {
			switch msg.Role {
			case llm.RoleAssistant:
				t.logger.Debug("task assistant message: %s", msg.Content.Text())
				for _, toolCall := range msg.ToolCalls {
					t.logger.Debug("task assistant %q tool call: %s", toolCall.Function.Name, toolCall.Function.Args)
				}
			case llm.RoleTool:
				t.logger.Debug("task %q tool result: %s", msg.Name, msg.Content.Text())
			}
		}
		if len(reporter.report) > 0 {
			t.logger.Debug("task completed successfully: %s", reporter.report)
			return taskToolResult{Report: reporter.report}.result()
		}
		if userPromptCount >= taskToolMaxUserPrompts {
			t.logger.Error("task did not complete after %d agent turns", taskToolMaxUserPrompts)
			return taskToolResult{Error: fmt.Sprintf("task did not complete after %d agent turns", taskToolMaxUserPrompts)}.result()
		}
		userPromptCount++
		history = append(history, t.promptForCompletionMessage())
		t.logger.Debug("injecting completion prompt (attempt %d/%d)", userPromptCount, taskToolMaxUserPrompts)
	}
	t.logger.Error("task did not complete after %d agent turns", taskToolMaxUserPrompts)
	return taskToolResult{Error: fmt.Sprintf("task did not complete after %d agent turns", taskToolMaxUserPrompts)}.result()
}

func (t *taskTool) systemMessage() llm.Message {
	text := strings.TrimSpace(`
You are an autonomous task execution agent operating in a sandboxed environment with file system access. Your mission is to complete assigned tasks efficiently and accurately.

## Core principles
1. Autonomous operation: You cannot request clarification. Work with the information provided.
2. Tool utilization: Use available tools (file system operations or LLM calls) when needed - don't just theorize.
3. Quality focus: Prioritize correctness and completeness over speed.
4. Task-focused: Follow any specific instructions provided in the task description for execution approach and reporting format.

## Execution approach
- Read the task description carefully for any specific instructions.
- Use a systematic approach: analyze, plan, execute, validate.
- Use available tools as needed for file operations or LLM calls.
- Document your work as you progress.

## Reporting
- Complete the task with a call to 'write_report'.
- Follow any specific reporting format mentioned in the task description.
- If no format specified, provide a clear summary of what was accomplished.
- Include details about files created/modified and any issues encountered.

## Error handling
- If you encounter errors, document them clearly in your report.
- Attempt reasonable workarounds when possible, but don't deviate far from the task description.
- If the task cannot be completed, explain why in your report.

Execute tasks with precision and professionalism.
	`)
	return llm.Message{
		Role:    llm.RoleSystem,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}

func (t *taskTool) initialUserMessage(description string) llm.Message {
	text := fmt.Sprintf(
		`
Your task is as follows:
<task_description>
%s
</task_description>

Important notes:
- This is an autonomous execution - no further clarification is possible.
- You have full file system access within the current working directory.
- If task cannot be completed, stop execution and explain why in your report.

Begin task execution now.
		`,
		strings.TrimSpace(description),
	)
	text = strings.TrimSpace(text)
	return llm.Message{
		Role:    llm.RoleUser,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}

func (t *taskTool) promptForCompletionMessage() llm.Message {
	text := strings.TrimSpace(`
You must complete your task and call the 'write_report' tool before ending your turn.

Continue working on the task:
- If you've finished the work, call 'write_report' with your findings.
- If you cannot complete the task, call 'write_report' explaining what prevented completion.
- Do not end your turn without calling 'write_report'.

Continue working now.
	`)
	return llm.Message{
		Role:    llm.RoleUser,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}

//--------------------------------------------------------------------------------------------------

type writeReportTool struct {
	report string
}

func (t *writeReportTool) Spec() (string, string, json.RawMessage) {
	return "write_report", "Submit the final task completion report", json.RawMessage(`{
		"type": "object",
		"properties": {
			"report": {
				"type": "string",
				"description": "The task completion report. Should follow any specific format mentioned in the task description, otherwise provide a clear summary of what was accomplished."
			}
		},
		"required": ["report"]
	}`)
}

func (t *writeReportTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		return `{"error": "invalid JSON arguments"}`, nil
	}
	if t.report != "" {
		return `{"error": "report already written"}`, nil
	}
	report := gjson.Get(args, "report").String()
	if report == "" {
		return `{"error": "report must not be empty"}`, nil
	}
	if len(report) > taskToolMaxReportLength {
		return fmt.Sprintf(`{"error": "report exceeds maximum length of %d characters"}`, taskToolMaxReportLength), nil
	}
	trimmedReport := strings.TrimSpace(report)
	if len(trimmedReport) < 10 {
		return `{"error": "report must contain at least 10 characters of meaningful content"}`, nil
	}
	t.report = trimmedReport
	return `{"ok": true}`, nil
}
