package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

const (
	taskToolExecTimeout     = 5 * time.Minute
	taskToolMaxAgents       = 10
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

var _ llm.Tool = (*taskTool)(nil)

type taskTool struct {
	logger                 logger.Logger
	exec                   func(context.Context, string) (int, string, string, error)
	openRouterToken        string
	fastButCapableModel    string
	thoroughButCostlyModel string
}

func NewTask(
	exec func(context.Context, string) (int, string, string, error),
	openRouterToken string,
	fastButCapableModel, thoroughButCostlyModel string,
) *taskTool {
	return &taskTool{
		logger:                 logger.NoOp(),
		exec:                   exec,
		openRouterToken:        openRouterToken,
		fastButCapableModel:    fastButCapableModel,
		thoroughButCostlyModel: thoroughButCostlyModel,
	}
}

func (t *taskTool) SetLogger(logger logger.Logger) *taskTool {
	t.logger = logger
	return t
}

//go:embed task_description.md
var taskToolDescription string

func (t *taskTool) Spec() (string, string, json.RawMessage) {
	return "task", strings.TrimSpace(taskToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"effort": {
				"type": "string",
				"enum": ["fast", "thorough"],
				"description": "The desired effort level for the task"
			},
			"prompt": {
				"type": "string",
				"description": "The task to be performed. Can include variables like {{file_path}} to be replaced per agent"
			},
			"agents": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "The unique identifier for the agent ('1', '2', etc.)"
						},
						"variables": {
							"type": "object",
							"description": "A map of variable names to values that can be used in the prompt (e.g. 'file_path': '/path/to/file.txt')"
						}
					},
					"required": ["id"]
				},
				"description": "A list of agents that can be used to perform the task, each with a unique identifier and optional variables"
			}
		},
		"required": ["effort", "prompt", "agents"]
	}`)
}

func (t *taskTool) Call(ctx context.Context, args string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, taskToolExecTimeout)
	defer cancel()
	if !gjson.Valid(args) {
		t.logger.Error("task tool called with invalid JSON arguments")
		return taskToolResult{Error: "invalid JSON arguments"}.result()
	}
	// parse and validate the arguments
	effort := gjson.Get(args, "effort").String()
	prompt := gjson.Get(args, "prompt").String()
	agentsData := gjson.Get(args, "agents")
	if prompt == "" {
		return taskToolResult{Error: "prompt cannot be empty"}.result()
	}
	if len(prompt) > taskToolMaxDescLength {
		return taskToolResult{Error: fmt.Sprintf("prompt exceeds maximum length of %d characters", taskToolMaxDescLength)}.result()
	}
	if !agentsData.Exists() || !agentsData.IsArray() {
		return taskToolResult{Error: "agents must be a non-empty array"}.result()
	}
	agents := agentsData.Array()
	if len(agents) == 0 {
		return taskToolResult{Error: "at least one agent must be specified"}.result()
	}
	if len(agents) > taskToolMaxAgents {
		return taskToolResult{Error: fmt.Sprintf("too many agents specified, maximum is %d", taskToolMaxAgents)}.result()
	}
	// determine which model to use based on effort level
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
	t.logger.Debug("executing task with effort %q and model %q for %d agents: %s", effort, modelName, len(agents), prompt)
	// run agents in parallel using errgroup
	g, gctx := errgroup.WithContext(ctx)
	results := make([]string, len(agents))
	for i, agentData := range agents {
		g.Go(func() error {
			agentID := gjson.Get(agentData.Raw, "id").String()
			if agentID == "" {
				return fmt.Errorf("agent at index %d missing required 'id' field", i)
			}
			// substitute variables in prompt for this agent
			agentPrompt := prompt
			if variablesData := gjson.Get(agentData.Raw, "variables"); variablesData.Exists() {
				variablesData.ForEach(func(key, value gjson.Result) bool {
					placeholder := fmt.Sprintf("{{%s}}", key.String())
					agentPrompt = strings.ReplaceAll(agentPrompt, placeholder, value.String())
					return true
				})
			}
			// check for unsubstituted variables
			if strings.Contains(agentPrompt, "{{") && strings.Contains(agentPrompt, "}}") {
				return fmt.Errorf("agent %q has unsubstituted variables in prompt: %s", agentID, agentPrompt)
			}
			// execute the agent with the substituted prompt
			result, err := t.runSingleAgent(gctx, modelName, agentID, agentPrompt)
			if err != nil {
				return fmt.Errorf("agent %q failed (effort: %s, model: %s): %w", agentID, effort, modelName, err)
			}
			results[i] = result
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.logger.Error("task execution failed: %s", err.Error())
		return taskToolResult{Error: fmt.Sprintf("task execution failed: %s", err.Error())}.result()
	}
	// combine results from all agents
	var combinedReport strings.Builder
	for i, result := range results {
		if i > 0 {
			combinedReport.WriteString("\n\n")
		}
		agentID := gjson.Get(agents[i].Raw, "id").String()
		combinedReport.WriteString(fmt.Sprintf("Agent %s:\n%s", agentID, result))
	}
	report := combinedReport.String()
	if len(report) > taskToolMaxReportLength {
		report = report[:taskToolMaxReportLength] + "... (truncated)"
	}
	t.logger.Debug("task completed successfully with %d agent results", len(results))
	return taskToolResult{Report: report}.result()
}

func (t *taskTool) runSingleAgent(ctx context.Context, modelName, agentID, prompt string) (string, error) {
	t.logger.Debug("starting agent %q with model %q: %s", agentID, modelName, prompt)
	// initialise the model with the tools
	model := llm.NewOpenRouter(t.logger, t.openRouterToken, modelName,
		llm.WithOpenRouterCacheEnabled(),
	)
	model.Register(NewBash(t.exec).SetLogger(t.logger))
	model.Register(NewFSList().SetLogger(t.logger))
	model.Register(NewFSRead().SetLogger(t.logger))
	model.Register(NewFSReplace().SetLogger(t.logger))
	model.Register(NewFSWrite().SetLogger(t.logger))
	model.Register(NewLLM(t.openRouterToken).SetLogger(t.logger))
	model.Register(NewThink().SetLogger(t.logger))
	// populate the conversation history with the system and initial user messages
	history := []llm.Message{
		t.systemMessage(),
		t.initialUserMessage(prompt),
	}
	// start running the agent in a loop
	userPromptCount := 1
	for userPromptCount <= taskToolMaxUserPrompts {
		events := model.Stream(ctx, history,
			llm.WithMaxTokens(16384),
			llm.WithMaxTurns(taskToolMaxTurns),
			llm.WithTemperature(0.7),
		)
		messages, _, err := llm.Rollup(events)
		if err != nil {
			return "", fmt.Errorf("agent %q stream failed: %w", agentID, err)
		}
		// append the messages to the history
		history = append(history, messages...)
		// find the last assistant message to use as the report
		var lastAssistantMessage string
		for i := len(messages) - 1; i >= 0; i-- {
			switch messages[i].Role {
			case llm.RoleAssistant:
				if len(messages[i].ToolCalls) > 0 {
					continue
				}
				lastAssistantMessage = messages[i].Content.Text()
			}
			if lastAssistantMessage != "" {
				break
			}
		}
		// if we got an assistant message, use it as the result
		if lastAssistantMessage != "" {
			t.logger.Debug("agent %q completed with result: %s", agentID, lastAssistantMessage)
			return lastAssistantMessage, nil
		}
		if userPromptCount >= taskToolMaxUserPrompts {
			return "", fmt.Errorf("agent %q did not complete after %d turns with model %q", agentID, taskToolMaxUserPrompts, modelName)
		}
		userPromptCount++
		history = append(history, t.completeUserMessage())
		t.logger.Debug("agent %q injecting completion prompt (attempt %d/%d)", agentID, userPromptCount, taskToolMaxUserPrompts)
	}
	return "", fmt.Errorf("agent %q did not complete after %d turns with model %q", agentID, taskToolMaxUserPrompts, modelName)
}

//go:embed task_system.md
var taskToolSystem string

func (t *taskTool) systemMessage() llm.Message {
	text := strings.TrimSpace(taskToolSystem)
	return llm.Message{
		Role:    llm.RoleSystem,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}

//go:embed task_user_initial.md
var taskToolUserInitial string

func (t *taskTool) initialUserMessage(description string) llm.Message {
	text := strings.TrimSpace(taskToolUserInitial)
	text = strings.ReplaceAll(text, "{{description}}", strings.TrimSpace(description))
	return llm.Message{
		Role:    llm.RoleUser,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}

//go:embed task_user_complete.md
var taskToolUserComplete string

func (t *taskTool) completeUserMessage() llm.Message {
	text := strings.TrimSpace(taskToolUserComplete)
	return llm.Message{
		Role:    llm.RoleUser,
		Content: llm.ContentParts{llm.NewTextContentPart(text)},
	}
}
