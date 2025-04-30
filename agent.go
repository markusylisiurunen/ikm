package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/model"
)

type AgentEvent any

type HistoryChangedAgentEvent struct {
}
type TurnCompletedAgentEvent struct {
}
type ErrAgentEvent struct {
	err error
}

type Agent struct {
	client               *model.OpenRouter
	events               chan AgentEvent
	active               bool
	history              []model.Message
	tools                []Tool
	totalCost            float64
	turnCost             float64
	lastTurnTokens       int
	currTurnTokens       int
	currTurnCachedTokens int
}

func NewAgent() *Agent {
	tools := []Tool{bashTool{}, patchTool{}, writeTool{}}
	return &Agent{
		client:               model.NewOpenRouter(env.OpenRouterKey, defaultModel.Name),
		events:               make(chan AgentEvent),
		active:               false,
		history:              []model.Message{},
		tools:                tools,
		totalCost:            0,
		turnCost:             0,
		lastTurnTokens:       0,
		currTurnTokens:       0,
		currTurnCachedTokens: 0,
	}
}

func (a *Agent) Reset() {
	a.client = model.NewOpenRouter(env.OpenRouterKey, defaultModel.Name)
	a.active = false
	a.history = []model.Message{}
	a.totalCost = 0
	a.turnCost = 0
	a.lastTurnTokens = 0
	a.currTurnTokens = 0
	a.currTurnCachedTokens = 0
}

func (a *Agent) StartTurn(ctx context.Context) error {
	if a.active {
		debugString("agent is already active")
		return fmt.Errorf("agent is already active")
	}
	if len(a.history) == 0 || a.history[len(a.history)-1].Role != "user" {
		debugString("last message is not from user")
		return fmt.Errorf("last message is not from user")
	}
	a.active = true
	a.events <- struct{}{}
	a.ContinueTurn(ctx)
	return nil
}

func (a *Agent) ContinueTurn(ctx context.Context) {
	if env.Stream {
		a.continueTurnStream(ctx)
	} else {
		a.continueTurnSync(ctx)
	}
}
func (a *Agent) continueTurnSync(ctx context.Context) {
	go func() {
		defer a.updateCacheControl()
		history := []model.Message{a.getSystemInstructions()}
		history = append(history, a.history...)
		answer, usage, err := a.client.Generate(ctx, history)
		if err != nil {
			debugString("error calling openrouter sync: %v", err)
			a.active = false
			a.events <- ErrAgentEvent{err}
			return
		}
		// a.totalCost += answer.Usage.Cost
		// a.turnCost = answer.Usage.Cost
		a.lastTurnTokens = a.currTurnTokens
		a.currTurnTokens = usage.PromptTokens + usage.CompletionTokens
		// a.currTurnCachedTokens = answer.Usage.PromptTokensDetails.CachedTokens
		a.history = append(a.history, answer)
		if err := a.executeToolCalls(ctx); err != nil {
			debugString("error executing tool calls: %v", err)
			a.active = false
			a.events <- ErrAgentEvent{err}
			return
		}
		a.active = len(answer.ToolCalls) > 0
		a.events <- TurnCompletedAgentEvent{}
	}()
}
func (a *Agent) continueTurnStream(ctx context.Context) {
	// go func() {
	// 	defer a.updateCacheControl()
	// 	history := []model.Message{a.getSystemInstructions()}
	// 	history = append(history, a.history...)
	// 	events := a.client.Stream(ctx, history)
	// 	messageIsPushed := false
	// 	atLeastOneTool := false
	// 	errReceived := false
	// 	chunksIsOpen := true
	// 	errsIsOpen := true
	// 	for chunksIsOpen || errsIsOpen {
	// 		select {
	// 		case chunk, ok := <-chunkChan:
	// 			if !ok {
	// 				chunksIsOpen = false
	// 				continue
	// 			}
	// 			if errReceived {
	// 				continue
	// 			}
	// 			if chunk.Error != nil {
	// 				errReceived = true
	// 				a.active = false
	// 				err := fmt.Errorf("error from model: %s; code: %d; metadata: %v",
	// 					chunk.Error.Message, chunk.Error.Code, chunk.Error.Metadata)
	// 				a.events <- ErrAgentEvent{err}
	// 				continue
	// 			}
	// 			if chunk.Usage != nil {
	// 				a.totalCost += chunk.Usage.Cost
	// 				a.turnCost = chunk.Usage.Cost
	// 				a.lastTurnTokens = a.currTurnTokens
	// 				a.currTurnTokens = chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
	// 				a.currTurnCachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
	// 			}
	// 			delta := chunk.Choices[0].Delta
	// 			if delta.Content == "" && len(delta.ToolCalls) == 0 {
	// 				continue
	// 			}
	// 			if !messageIsPushed {
	// 				messageIsPushed = true
	// 				a.history = append(a.history, OpenRouter_Message{
	// 					Role:         "assistant",
	// 					ContentParts: OpenRouter_Message_ContentParts{},
	// 				})
	// 			}
	// 			lasIdx := len(a.history) - 1
	// 			a.history[lasIdx].ContentParts.Append(delta.Content)
	// 			for _, tc := range delta.ToolCalls {
	// 				if tc.Type != "function" {
	// 					debugString("tool call type %s not supported", tc.Type)
	// 					continue
	// 				}
	// 				atLeastOneTool = true
	// 				var toolCallIdx int = -1
	// 				for idx, toolCall := range a.history[lasIdx].ToolCalls {
	// 					if toolCall.Index == tc.Index {
	// 						toolCallIdx = idx
	// 						break
	// 					}
	// 				}
	// 				if toolCallIdx == -1 {
	// 					a.history[lasIdx].ToolCalls = append(a.history[lasIdx].ToolCalls, tc)
	// 					continue
	// 				}
	// 				a.history[lasIdx].ToolCalls[toolCallIdx].Function.Arguments += tc.Function.Arguments
	// 			}
	// 			a.events <- HistoryChangedAgentEvent{}
	// 		case err, ok := <-errChan:
	// 			if !ok {
	// 				errsIsOpen = false
	// 				continue
	// 			}
	// 			if err != nil {
	// 				debugString("error from openrouter stream: %v", err)
	// 				errReceived = true
	// 				a.active = false
	// 				a.events <- ErrAgentEvent{err}
	// 			}
	// 		}
	// 	}
	// 	if errReceived {
	// 		return
	// 	}
	// 	if err := a.executeToolCalls(ctx); err != nil {
	// 		debugString("error executing tool calls: %v", err)
	// 		a.active = false
	// 		a.events <- ErrAgentEvent{err}
	// 		return
	// 	}
	// 	a.active = atLeastOneTool
	// 	a.events <- TurnCompletedAgentEvent{}
	// }()
}

func (a *Agent) executeToolCalls(ctx context.Context) error {
	if !a.active || len(a.history) == 0 {
		return nil
	}
	last := a.history[len(a.history)-1]
	if len(last.ToolCalls) == 0 {
		return nil
	}
	for _, tc := range last.ToolCalls {
		if tc.Type != "function" {
			return fmt.Errorf("tool call type %s not supported", tc.Type)
		}
		debugAny(map[string]any{"msg": "processing a tool call", "tool_call": tc})
		tool := a.getTool(tc.Function.Name)
		if tool == nil {
			return fmt.Errorf("tool %s not found", tc.Function.Name)
		}
		result, err := tool.Execute(ctx, tc.Function.Arguments)
		msg := OpenRouter_Message{
			Role:       "tool",
			ContentStr: "",
			ToolCallID: &tc.ID,
			ToolName:   &tc.Function.Name,
		}
		if err != nil {
			debugString("error executing tool %s: %v", tc.Function.Name, err)
			msg.ContentStr = fmt.Sprintf("Error: %v", err)
		} else {
			debugString("tool %s executed successfully: %q", tc.Function.Name, result)
			msg.ContentStr = result
		}
		a.history = append(a.history, msg)
	}
	return nil
}

func (a *Agent) updateCacheControl() {
	const UPDATE_CACHE_EVERY_N_TOKENS = 7_500
	lastTurnPtr := math.Floor(float64(a.lastTurnTokens) / UPDATE_CACHE_EVERY_N_TOKENS)
	currTurnPtr := math.Floor(float64(a.currTurnTokens) / UPDATE_CACHE_EVERY_N_TOKENS)
	if lastTurnPtr == currTurnPtr {
		return
	}
	for _, msg := range a.history {
		msg.ContentParts.Uncache()
	}
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == "assistant" {
			a.history[i].ContentParts.Cache()
			break
		}
	}
}

func (a *Agent) getSystemInstructions() OpenRouter_Message {
	custom := a.getCustomInstructions()
	switch env.Mode {
	case ModeAgent:
		content := strings.TrimSpace(agentSystemPrompt)
		// append custom instructions
		if custom != "" {
			content += "\n\nIf any custom instructions conflict with the general instructions provided earlier, you must follow the custom instructions.\n"
			content += "\nPlease refer to the user-provided project-specific custom instructions below:\n"
			content += fmt.Sprintf("<custom_instructions>\n%s\n</custom_instructions>", custom)
		}
		// // append current working directory structure
		// code, stdout, _, err := runInBashDocker(context.Background(), "tree --gitignore -n")
		// if err != nil || code != 0 {
		// 	panic(fmt.Sprintf("error executing command (exit code: %d): %v", code, err))
		// }
		// content += "\n\nPlease refer to the current (consider as ground-truth) working directory structure below:\n"
		// content += fmt.Sprintf("<current_working_directory>\n%s\n</current_working_directory>", strings.TrimSpace(stdout))
		parts := OpenRouter_Message_ContentParts{{Type: "text", Text: content}}
		parts.Cache()
		return OpenRouter_Message{Role: "system", ContentParts: parts}
	case ModeDev:
		content := strings.TrimSpace(devSystemPrompt)
		// append custom instructions
		if custom != "" {
			content += "\n\nIf any custom instructions conflict with the general instructions provided earlier, you must follow the custom instructions.\n"
			content += "\nPlease refer to the user-provided project-specific custom instructions below:\n"
			content += fmt.Sprintf("<custom_instructions>\n%s\n</custom_instructions>", custom)
		}
		// // append current working directory structure
		// code, stdout, _, err := runInBashDocker(context.Background(), "tree --gitignore -n")
		// if err != nil || code != 0 {
		// 	panic(fmt.Sprintf("error executing command (exit code: %d): %v", code, err))
		// }
		// content += "\n\nPlease refer to the current (consider as ground-truth) working directory structure below:\n"
		// content += fmt.Sprintf("<current_working_directory>\n%s\n</current_working_directory>", strings.TrimSpace(stdout))
		parts := OpenRouter_Message_ContentParts{{Type: "text", Text: content}}
		parts.Cache()
		return OpenRouter_Message{Role: "system", ContentParts: parts}
	case ModeRaw:
		content := "You may have access to tools, but you should never use them; act as if you don't have any tools."
		return OpenRouter_Message{Role: "system", ContentParts: OpenRouter_Message_ContentParts{{Type: "text", Text: content}}}
	default:
		panic(fmt.Sprintf("unknown mode: %s", env.Mode))
	}
}

func (a *Agent) getCustomInstructions() string {
	_, err := os.Stat(".ikm/instructions.md")
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		debugString("error reading custom instructions: %v", err)
		return ""
	}
	content, err := os.ReadFile(".ikm/instructions.md")
	if err != nil {
		debugString("error reading custom instructions: %v", err)
		return ""
	}
	return strings.TrimSpace(string(content))
}

func (a *Agent) getTool(name string) Tool {
	for _, t := range a.tools {
		if t.Definition().Function.Name == name {
			return t
		}
	}
	return nil
}
