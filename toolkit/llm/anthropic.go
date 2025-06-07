package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"golang.org/x/sync/errgroup"
)

var _ Model = (*Anthropic)(nil)

type AnthropicOption func(*Anthropic)

type Anthropic struct {
	logger logger.Logger
	token  string
	model  string
	tools  []Tool
	usage  *anthropic_Response_Usage
}

func NewAnthropic(logger logger.Logger, token, model string, opts ...AnthropicOption) *Anthropic {
	a := &Anthropic{logger: logger, token: token, model: model}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Anthropic) Register(tool Tool) {
	if tool != nil {
		a.tools = append(a.tools, tool)
	}
}

func (a *Anthropic) Stream(ctx context.Context, messages []Message, opts ...StreamOption) <-chan Event {
	config := a.generationConfig(opts...)
	return a.streamTurns(ctx, messages, config)
}
func (a *Anthropic) streamTurns(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		cloned := make([]Message, len(messages))
		copy(cloned, messages)
		for turn := range config.maxTurns {
			select {
			case <-ctx.Done():
				ch <- &ErrorEvent{Err: ctx.Err()}
				return
			default:
			}
			out := tee(a.streamTurn(ctx, cloned, config), ch)
			builder := newMessageBuilder()
			for event := range out {
				builder.process(event)
			}
			messages, _, err := builder.result()
			if err != nil {
				ch <- &ErrorEvent{Err: fmt.Errorf("error processing events: %w", err)}
				return
			}
			if len(messages) != 1 {
				ch <- &ErrorEvent{Err: fmt.Errorf("expected exactly one message, got %d", len(messages))}
				return
			}
			if len(messages[0].ToolCalls) == 0 {
				return
			}
			cloned = append(cloned, messages[0])
			if len(messages[0].ToolCalls) > 0 {
				toolResultEvents := make([]*ToolResultEvent, len(messages[0].ToolCalls))
				g, gctx := errgroup.WithContext(ctx)
				for idx, toolCall := range messages[0].ToolCalls {
					g.Go(func() error {
						var tool Tool
						for _, t := range a.tools {
							if name, _, _ := t.Spec(); name == toolCall.Function.Name {
								tool = t
								break
							}
						}
						if tool == nil {
							return fmt.Errorf("tool %s not found", toolCall.Function.Name)
						}
						result, err := tool.Call(gctx, toolCall.Function.Args)
						toolResultEvents[idx] = &ToolResultEvent{ID: toolCall.ID, Result: result, Error: err}
						return nil
					})
				}
				if err := g.Wait(); err != nil {
					ch <- &ErrorEvent{Err: fmt.Errorf("error executing tool calls: %w", err)}
					return
				}
				for idx, event := range toolResultEvents {
					if event == nil {
						ch <- &ErrorEvent{Err: fmt.Errorf("tool call %d result is nil", idx)}
						return
					}
					ch <- event
					msg := Message{
						Role:       RoleTool,
						Name:       messages[0].ToolCalls[idx].Function.Name,
						ToolCallID: messages[0].ToolCalls[idx].ID,
					}
					if event.Error != nil {
						msg.Content = ContentParts{NewTextContentPart("Error: " + event.Error.Error())}
					} else {
						msg.Content = ContentParts{NewTextContentPart(event.Result)}
					}
					cloned = append(cloned, msg)
				}
			}
			if turn >= config.maxTurns-1 || (config.stopCondition != nil && config.stopCondition(turn, cloned)) {
				return
			}
		}
	}()
	return ch
}
func (a *Anthropic) streamTurn(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	a.usage = nil
	ch := make(chan Event)
	go func() {
		defer close(ch)
		resp, err := a.request(ctx, messages, config)
		if err != nil {
			ch <- &ErrorEvent{Err: err}
			return
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				ch <- &ErrorEvent{Err: fmt.Errorf("error reading response body: %w", err)}
			} else {
				ch <- &ErrorEvent{Err: fmt.Errorf("non-ok status (%d) from Anthropic: %s", resp.StatusCode, string(body))}
			}
			return
		}
		toolCallBuffer := make([]*ToolUseEvent, 32)
		var currentEvent string
		var currentData string
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			select {
			case <-ctx.Done():
				ch <- &ErrorEvent{Err: ctx.Err()}
				return
			default:
			}
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				ch <- &ErrorEvent{Err: fmt.Errorf("error reading stream: %w", err)}
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				if currentEvent != "" && currentData != "" {
					a.processSSEEvent(currentEvent, currentData, ch, toolCallBuffer)
				}
				currentEvent = ""
				currentData = ""
				continue
			}
			if after, ok := strings.CutPrefix(line, "event: "); ok {
				currentEvent = after
			} else if after, ok := strings.CutPrefix(line, "data: "); ok {
				currentData = after
			}
		}
		if currentEvent != "" && currentData != "" {
			a.processSSEEvent(currentEvent, currentData, ch, toolCallBuffer)
		}
	}()
	return ch
}

func (a *Anthropic) request(ctx context.Context, messages []Message, config streamConfig) (*http.Response, error) {
	payload := anthropic_Request{
		MaxTokens:   config.maxTokens,
		Messages:    []anthropic_Message{},
		Model:       a.model,
		Stream:      true,
		System:      "",
		Temperature: config.temperature,
		Thinking:    nil,
		Tools:       nil,
	}
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			payload.System = msg.Content.Text()
			continue
		}
		var m anthropic_Message
		if err := m.from(msg); err != nil {
			return nil, fmt.Errorf("error converting message: %w", err)
		}
		payload.Messages = append(payload.Messages, m)
	}
	a.injectCacheControl(payload.Messages)
	if config.reasoningEffort > 0 {
		switch config.reasoningEffort {
		case 1:
			payload.Thinking = &anthropic_Request_Thinking{
				Type:         "enabled",
				BudgetTokens: int(math.Round(float64(config.maxTokens) * 0.2)),
			}
		case 2:
			payload.Thinking = &anthropic_Request_Thinking{
				Type:         "enabled",
				BudgetTokens: int(math.Round(float64(config.maxTokens) * 0.5)),
			}
		default:
			payload.Thinking = &anthropic_Request_Thinking{
				Type:         "enabled",
				BudgetTokens: int(math.Round(float64(config.maxTokens) * 0.8)),
			}
		}
	}
	if len(a.tools) > 0 {
		payload.Tools = make([]anthropic_Request_Tool, len(a.tools))
		for i, tool := range a.tools {
			name, description, inputSchema := tool.Spec()
			payload.Tools[i] = anthropic_Request_Tool{
				Name:        name,
				Description: description,
				InputSchema: json.RawMessage(inputSchema),
			}
		}
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}
	a.logger.Debug("Anthropic request payload: %s", data.String())
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, "https://api.anthropic.com/v1/messages", &data)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.token)
	client := &http.Client{Timeout: 300 * time.Second /* 5 min */}
	return client.Do(req)
}

func (a *Anthropic) processSSEEvent(event, data string, ch chan<- Event, toolCallBuffer []*ToolUseEvent) {
	switch event {
	case "message_start":
		var msgStart anthropic_Response_MessageStart
		if err := json.Unmarshal([]byte(data), &msgStart); err != nil {
			a.logger.Error("failed to parse message_start: %v", err)
			return
		}
		if usage := msgStart.Message.Usage; usage != nil {
			a.logger.Debug("Anthropic usage: %d prompt tokens (cached %d or %.2f%%)",
				usage.InputTokens+usage.CacheCreationInputTokens+usage.CacheReadInputTokens,
				usage.CacheReadInputTokens,
				float64(usage.CacheReadInputTokens)/float64(usage.InputTokens+usage.CacheCreationInputTokens+usage.CacheReadInputTokens)*100.0,
			)
			a.usage = usage
		}
	case "content_block_start":
		var blockStart anthropic_Response_ContentBlockStart
		if err := json.Unmarshal([]byte(data), &blockStart); err != nil {
			a.logger.Error("failed to parse content_block_start: %v", err)
			return
		}
		if blockStart.ContentBlock.Type == "tool_use" {
			for i := range toolCallBuffer {
				if toolCallBuffer[i] == nil {
					toolCallBuffer[i] = &ToolUseEvent{
						ID:       blockStart.ContentBlock.ID,
						Index:    i,
						FuncName: blockStart.ContentBlock.Name,
						FuncArgs: "",
					}
					break
				}
			}
		}
	case "content_block_delta":
		var blockDelta anthropic_Response_ContentBlockDelta
		if err := json.Unmarshal([]byte(data), &blockDelta); err != nil {
			a.logger.Error("failed to parse content_block_delta: %v", err)
			return
		}
		if blockDelta.Delta.Type == "text_delta" && blockDelta.Delta.Text != "" {
			ch <- &ContentDeltaEvent{
				Content: blockDelta.Delta.Text,
			}
		} else if blockDelta.Delta.Type == "input_json_delta" && blockDelta.Delta.PartialJSON != "" {
			lastNonNilToolBufferIndex := -1
			for i, toolCall := range toolCallBuffer {
				if toolCall != nil && toolCall.FuncName != "" {
					lastNonNilToolBufferIndex = i
				}
			}
			if lastNonNilToolBufferIndex == -1 {
				return
			}
			if lastNonNilToolBufferIndex < len(toolCallBuffer) && toolCallBuffer[lastNonNilToolBufferIndex] != nil {
				toolCallBuffer[lastNonNilToolBufferIndex].FuncArgs += blockDelta.Delta.PartialJSON
			}
		} else if blockDelta.Delta.Type == "thinking_delta" && blockDelta.Delta.Thinking != "" {
			ch <- &ThinkingDeltaEvent{Thinking: blockDelta.Delta.Thinking}
		} else if blockDelta.Delta.Type == "signature_delta" && blockDelta.Delta.Signature != "" {
			ch <- &ThinkingDeltaEvent{Signature: blockDelta.Delta.Signature}
		}
	case "content_block_stop":
		return
	case "message_delta":
		var msgDelta anthropic_Response_MessageDelta
		if err := json.Unmarshal([]byte(data), &msgDelta); err != nil {
			a.logger.Error("failed to parse message_delta: %v", err)
			return
		}
		if a.usage != nil && msgDelta.Usage != nil {
			a.usage.OutputTokens += msgDelta.Usage.OutputTokens
		}
	case "ping":
		return
	case "message_stop":
		for _, toolCall := range toolCallBuffer {
			if toolCall == nil {
				break
			}
			ch <- toolCall
		}
		if a.usage != nil {
			ch <- &UsageEvent{Usage: Usage{
				PromptTokens:     a.usage.InputTokens + a.usage.CacheCreationInputTokens + a.usage.CacheReadInputTokens,
				CompletionTokens: a.usage.OutputTokens,
				TotalCost:        a.estimateCost(*a.usage),
			}}
		}
	default:
		a.logger.Error("unknown SSE event type: %s", event)
	}
}

func (a *Anthropic) injectCacheControl(messages []anthropic_Message) {
	// inject the cache control into the last available user message text part
	userMessageCached := false
	for i := len(messages) - 1; i >= 0; i-- {
		if userMessageCached {
			break
		}
		if messages[i].Role != "user" || len(messages[i].Content) == 0 {
			continue
		}
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			part, ok := messages[i].Content[j].(anthropic_Message_Text)
			if !ok {
				continue
			}
			part.CacheControl = &anthropic_Message_CacheControl{Type: "ephemeral"}
			messages[i].Content[j] = part
			userMessageCached = true
			break
		}
	}
	// inject the cache control into the last available user message tool result part
	toolResultCached := false
	for i := len(messages) - 1; i >= 0; i-- {
		if toolResultCached {
			break
		}
		if messages[i].Role != "user" || len(messages[i].Content) == 0 {
			continue
		}
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			part, ok := messages[i].Content[j].(anthropic_Message_ToolResult)
			if !ok {
				continue
			}
			part.CacheControl = &anthropic_Message_CacheControl{Type: "ephemeral"}
			messages[i].Content[j] = part
			toolResultCached = true
			break
		}
	}
}

func (a *Anthropic) generationConfig(opts ...StreamOption) streamConfig {
	c := streamConfig{
		maxTokens:       8192,
		maxTurns:        1,
		reasoningEffort: 0,
		temperature:     1.0,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

func (a *Anthropic) estimateCost(usage anthropic_Response_Usage) float64 {
	type costConfig struct {
		inputTokens      float64
		cacheReadTokens  float64
		cacheWriteTokens float64
		outputTokens     float64
	}
	costs := map[string]costConfig{
		"claude-sonnet-4-20250514": {
			inputTokens:      3,
			cacheReadTokens:  0.3,
			cacheWriteTokens: 3.75,
			outputTokens:     15,
		},
		"claude-opus-4-20250514": {
			inputTokens:      15,
			cacheReadTokens:  1.5,
			cacheWriteTokens: 18.75,
			outputTokens:     75,
		},
	}
	var cost *costConfig
	if c, ok := costs[a.model]; ok {
		cost = &c
	} else {
		a.logger.Error("no cost information available for model %s, using intentionally high default (2x Opus) values", a.model)
		cost = &costConfig{
			inputTokens:      30,
			cacheReadTokens:  3,
			cacheWriteTokens: 37.5,
			outputTokens:     150,
		}
	}
	millionInputTokens := float64(usage.InputTokens) / 1000000.0
	millionCacheCreationInputTokens := float64(usage.CacheCreationInputTokens) / 1000000.0
	millionCacheReadInputTokens := float64(usage.CacheReadInputTokens) / 1000000.0
	millionOutputTokens := float64(usage.OutputTokens) / 1000000.0
	// compute the cost with and without cache
	costWithCache := millionInputTokens*cost.inputTokens +
		millionCacheReadInputTokens*cost.cacheReadTokens +
		millionCacheCreationInputTokens*cost.cacheWriteTokens +
		millionOutputTokens*cost.outputTokens
	costWithoutCache := (millionInputTokens+millionCacheCreationInputTokens+millionCacheReadInputTokens)*cost.inputTokens +
		millionOutputTokens*cost.outputTokens
	a.logger.Debug("Anthropic cost estimate: $%.3f (without cache), $%.3f (with cache), saved $%.3f or %.2f%%",
		costWithoutCache, costWithCache, costWithoutCache-costWithCache, (costWithoutCache-costWithCache)/costWithoutCache*100)
	return costWithCache
}

// helper types ------------------------------------------------------------------------------------

// messages
type anthropic_Message_CacheControl struct {
	Type string `json:"type"`
}
type anthropic_Message_Thinking struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}
type anthropic_Message_Text struct {
	Type         string                          `json:"type"`
	Text         string                          `json:"text"`
	CacheControl *anthropic_Message_CacheControl `json:"cache_control,omitzero"`
}
type anthropic_Message_ToolUse struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}
type anthropic_Message_ToolResult struct {
	Type         string                          `json:"type"`
	ToolUseID    string                          `json:"tool_use_id"`
	Content      string                          `json:"content"`
	CacheControl *anthropic_Message_CacheControl `json:"cache_control,omitzero"`
}
type anthropic_Message struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

func (m *anthropic_Message) from(msg Message) error {
	switch msg.Role {
	case RoleSystem:
		return fmt.Errorf("Anthropic does not support system messages")
	case RoleAssistant:
		m.Role = "assistant"
	case RoleUser, RoleTool:
		m.Role = "user"
	default:
		return fmt.Errorf("unexpected message role: %s", msg.Role)
	}
	if msg.Role == RoleTool {
		m.Content = append(m.Content, anthropic_Message_ToolResult{
			Type:      "tool_result",
			ToolUseID: msg.ToolCallID,
			Content:   msg.Content.Text(),
		})
	} else {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case ThinkingContentPart:
				m.Content = append(m.Content, anthropic_Message_Thinking{
					Type:      "thinking",
					Thinking:  p.Thinking,
					Signature: p.Signature,
				})
			case TextContentPart:
				m.Content = append(m.Content, anthropic_Message_Text{
					Type: "text",
					Text: p.Text,
				})
			case ImageContentPart:
				return fmt.Errorf("image content part currently not supported in Anthropic messages")
			case FileContentPart:
				return fmt.Errorf("file content part currently not supported in Anthropic messages")
			default:
				return fmt.Errorf("unexpected content part type: %T", part)
			}
		}
	}
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			input := "{}"
			if tc.Function.Args != "" {
				input = tc.Function.Args
			}
			m.Content = append(m.Content, anthropic_Message_ToolUse{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(input),
			})
		}
	}
	return nil
}

// requests
type anthropic_Request_Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}
type anthropic_Request_Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type anthropic_Request struct {
	MaxTokens   int                         `json:"max_tokens"`
	Messages    []anthropic_Message         `json:"messages"`
	Model       string                      `json:"model"`
	Stream      bool                        `json:"stream"`
	System      string                      `json:"system,omitzero"`
	Temperature float64                     `json:"temperature"`
	Thinking    *anthropic_Request_Thinking `json:"thinking,omitzero"`
	Tools       []anthropic_Request_Tool    `json:"tools,omitzero"`
}

// responses
type anthropic_Response_Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type anthropic_Response_Message struct {
	ID    string                    `json:"id"`
	Type  string                    `json:"type"`
	Role  string                    `json:"role"`
	Model string                    `json:"model"`
	Usage *anthropic_Response_Usage `json:"usage"`
}

type anthropic_Response_MessageStart struct {
	Type    string                     `json:"type"`
	Message anthropic_Response_Message `json:"message"`
}

type anthropic_Response_ContentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type anthropic_Response_ContentBlockStart struct {
	Type         string                          `json:"type"`
	Index        int                             `json:"index"`
	ContentBlock anthropic_Response_ContentBlock `json:"content_block"`
}

type anthropic_Response_Delta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

type anthropic_Response_ContentBlockDelta struct {
	Type  string                   `json:"type"`
	Index int                      `json:"index"`
	Delta anthropic_Response_Delta `json:"delta"`
}

type anthropic_Response_MessageDelta struct {
	Type  string                    `json:"type"`
	Delta map[string]any            `json:"delta"`
	Usage *anthropic_Response_Usage `json:"usage"`
}
