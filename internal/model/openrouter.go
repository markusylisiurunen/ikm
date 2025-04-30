package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var _ Model = (*OpenRouter)(nil)

type OpenRouter struct {
	token string
	model string
	tools []Tool
}

func NewOpenRouter(token, model string) *OpenRouter {
	return &OpenRouter{token: token, model: model}
}

func (o *OpenRouter) Register(tool Tool) {
	if tool != nil {
		o.tools = append(o.tools, tool)
	}
}

func (o *OpenRouter) Stream(ctx context.Context, messages []Message, opts ...StreamOption) <-chan Event {
	config := o.generationConfig(opts...)
	return o.streamTurns(ctx, messages, config)
}
func (o *OpenRouter) streamTurns(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		cloned := make([]Message, len(messages))
		copy(cloned, messages)
		for turn := range config.maxTurns {
			out := tee(o.streamTurn(ctx, cloned, config), ch)
			builder := newMessageBuilder()
			for event := range out {
				builder.process(event)
			}
			messages, _, err := builder.result()
			if err != nil || len(messages) != 1 || len(messages[0].ToolCalls) == 0 || turn >= config.maxTurns-1 {
				return
			}
			cloned = append(cloned, messages[0])
			for _, toolCall := range messages[0].ToolCalls {
				var tool Tool
				for _, t := range o.tools {
					if name, _, _ := t.Spec(); name == toolCall.Function.Name {
						tool = t
						break
					}
				}
				if tool == nil {
					ch <- &ErrorEvent{Err: fmt.Errorf("tool %s not found", toolCall.Function.Name)}
					return
				}
				result, err := tool.Call(ctx, toolCall.Function.Args)
				ch <- &ToolResultEvent{ID: toolCall.ID, Result: result, Error: err}
				msg := Message{
					Role:       RoleTool,
					Name:       toolCall.Function.Name,
					ToolCallID: toolCall.ID,
				}
				if err != nil {
					msg.Content = ContentParts{NewTextContentPart("Error: " + err.Error())}
				} else {
					msg.Content = ContentParts{NewTextContentPart(result)}
				}
				cloned = append(cloned, msg)
			}
		}
	}()
	return ch
}
func (o *OpenRouter) streamTurn(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		resp, err := o.request(ctx, messages, config)
		if err != nil {
			ch <- &ErrorEvent{Err: err}
			return
		}
		defer resp.Body.Close()
		toolCallBuffer := make([]*ToolUseEvent, 10)
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
				continue
			}
			var raw string
			if strings.HasPrefix(line, "data: ") {
				raw = strings.TrimPrefix(line, "data: ")
			} else if strings.HasPrefix(line, "{") {
				raw = line
			}
			if raw == "" {
				continue
			}
			if raw == "[DONE]" {
				break
			}
			var chunk openRouter_Chunk
			if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
				continue
			}
			if chunk.Error != nil {
				ch <- &ErrorEvent{Err: &StreamError{
					Code:     chunk.Error.Code,
					Message:  chunk.Error.Message,
					Metadata: chunk.Error.Metadata,
				}}
				return
			}
			if chunk.Usage != nil {
				ch <- &UsageEvent{Usage: Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalCost:        chunk.Usage.Cost,
				}}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta != nil && choice.Delta.Content != "" {
				ch <- &ContentDeltaEvent{Content: choice.Delta.Content}
			}
			if choice.Delta != nil && choice.Delta.ToolCalls != nil {
				for _, toolCall := range choice.Delta.ToolCalls {
					index := toolCall.Index
					if index < 0 || index >= len(toolCallBuffer) {
						panic("tool call index out of range")
					}
					if toolCallBuffer[index] == nil {
						toolCallBuffer[index] = &ToolUseEvent{
							ID:       toolCall.ID,
							Index:    index,
							FuncName: toolCall.Function.Name,
							FuncArgs: toolCall.Function.Arguments,
						}
					} else {
						toolCallBuffer[index].FuncArgs += toolCall.Function.Arguments
					}
				}
			}
			select {
			case <-ctx.Done():
				ch <- &ErrorEvent{Err: ctx.Err()}
				return
			default:
			}
		}
		for _, toolCall := range toolCallBuffer {
			if toolCall != nil {
				ch <- toolCall
			}
		}
	}()
	return ch
}

func (o *OpenRouter) request(
	ctx context.Context, messages []Message, config streamConfig,
) (*http.Response, error) {
	payload := openRouter_Request{
		MaxTokens:   config.maxTokens,
		Messages:    []openRouter_Message{},
		Model:       o.model,
		Reasoning:   nil,
		Stream:      true,
		Temperature: config.temperature,
		Tools:       nil,
		Usage:       openRouter_Request_Usage{Include: true},
	}
	for _, msg := range messages {
		var m openRouter_Message
		if err := m.from(msg); err != nil {
			return nil, fmt.Errorf("error converting message: %w", err)
		}
		payload.Messages = append(payload.Messages, m)
	}
	if config.reasoningEffort > 0 {
		switch config.reasoningEffort {
		case 1:
			payload.Reasoning = &openRouter_Request_Reasoning{Effort: "low"}
		case 2:
			payload.Reasoning = &openRouter_Request_Reasoning{Effort: "medium"}
		default:
			payload.Reasoning = &openRouter_Request_Reasoning{Effort: "high"}
		}
	}
	if len(o.tools) > 0 {
		payload.Tools = make([]openRouter_Request_Tool, len(o.tools))
		for i, tool := range o.tools {
			name, description, parameters := tool.Spec()
			payload.Tools[i] = openRouter_Request_Tool{
				Type: "function",
				Function: &openRouter_Request_Tool_Function{
					Name:        name,
					Description: description,
					Parameters:  parameters,
				},
			}
		}
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", &data)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 300 * time.Second /* 5 min */}
	return client.Do(req)
}

func (o *OpenRouter) generationConfig(opts ...StreamOption) streamConfig {
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

// helper types ------------------------------------------------------------------------------------

// messages
type openRouter_Message_ToolCall_Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type openRouter_Message_ToolCall struct {
	Index    int                                   `json:"index"`
	ID       string                                `json:"id"`
	Type     string                                `json:"type"`
	Function *openRouter_Message_ToolCall_Function `json:"function,omitempty"`
}

type openRouter_Message_ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openRouter_Message_ContentParts []openRouter_Message_ContentPart

func (c *openRouter_Message_ContentParts) appendText(text string) {
	if c == nil {
		return
	}
	if len(*c) == 0 {
		*c = append(*c, openRouter_Message_ContentPart{Type: "text", Text: text})
	} else if p := (*c)[len(*c)-1]; p.Type == "text" {
		p.Text += text
		(*c)[len(*c)-1] = p
	} else {
		*c = append(*c, openRouter_Message_ContentPart{Type: "text", Text: text})
	}
}

type openRouter_Message struct {
	Role          string                          `json:"role"`
	ContentParts  openRouter_Message_ContentParts `json:"-"`
	ContentString string                          `json:"-"`
	ToolCalls     []openRouter_Message_ToolCall   `json:"tool_calls,omitempty"`
	Name          *string                         `json:"name,omitempty"`
	ToolCallID    *string                         `json:"tool_call_id,omitempty"`
}

func (m *openRouter_Message) from(msg Message) error {
	m.Role = string(msg.Role)
	m.ContentParts = nil
	m.ContentString = ""
	for _, part := range msg.Content {
		switch p := part.(type) {
		case TextContentPart:
			if msg.Role == RoleAssistant || msg.Role == RoleTool {
				m.ContentString += p.Text
			} else {
				m.ContentParts.appendText(p.Text)
			}
		default:
			return fmt.Errorf("unexpected content part type: %T", part)
		}
	}
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, openRouter_Message_ToolCall{
				Index: tc.Index,
				ID:    tc.ID,
				Type:  "function",
				Function: &openRouter_Message_ToolCall_Function{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Args,
				},
			})
		}
	}
	if msg.Role == RoleTool {
		m.Name = &msg.Name
		m.ToolCallID = &msg.ToolCallID
	}
	return nil
}

func (m *openRouter_Message) UnmarshalJSON(data []byte) error {
	type Alias openRouter_Message
	aux := &struct {
		Content json.RawMessage `json:"content"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("error unmarshalling OpenRouter message: %w", err)
	}
	if len(aux.Content) > 0 {
		if aux.Content[0] == '"' {
			var contentStr string
			if err := json.Unmarshal(aux.Content, &contentStr); err != nil {
				return fmt.Errorf("error unmarshalling OpenRouter message content as string: %w", err)
			}
			m.ContentParts = nil
			m.ContentString = contentStr
		} else if aux.Content[0] == '[' {
			var contentParts openRouter_Message_ContentParts
			if err := json.Unmarshal(aux.Content, &contentParts); err != nil {
				return fmt.Errorf("error unmarshalling OpenRouter message content as array: %w", err)
			}
			m.ContentParts = contentParts
			m.ContentString = ""
		} else {
			return fmt.Errorf("unexpected content type in OpenRouter message: %s", aux.Content)
		}
	}
	return nil
}

func (m openRouter_Message) MarshalJSON() ([]byte, error) {
	type Alias openRouter_Message
	aux := &struct {
		Content any `json:"content,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(&m),
	}
	if len(m.ContentParts) > 0 {
		aux.Content = m.ContentParts
	} else if m.ContentString != "" {
		aux.Content = m.ContentString
	}
	return json.Marshal(aux)
}

// requests
type openRouter_Request_Tool_Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type openRouter_Request_Tool struct {
	Type     string                            `json:"type"`
	Function *openRouter_Request_Tool_Function `json:"function,omitempty"`
}

type openRouter_Request_Reasoning struct {
	Effort string `json:"effort"`
}

type openRouter_Request_Usage struct {
	Include bool `json:"include"`
}

type openRouter_Request struct {
	MaxTokens   int                           `json:"max_tokens"`
	Messages    []openRouter_Message          `json:"messages"`
	Model       string                        `json:"model"`
	Reasoning   *openRouter_Request_Reasoning `json:"reasoning,omitempty"`
	Stream      bool                          `json:"stream"`
	Temperature float64                       `json:"temperature"`
	Tools       []openRouter_Request_Tool     `json:"tools,omitempty"`
	Usage       openRouter_Request_Usage      `json:"usage"`
}

// stream responses
type openRouter_Chunk_Choice_Delta struct {
	Role      string                        `json:"role"`
	Content   string                        `json:"content"`
	ToolCalls []openRouter_Message_ToolCall `json:"tool_calls"`
}
type openRouter_Chunk_Choice struct {
	Delta        *openRouter_Chunk_Choice_Delta `json:"delta"`
	FinishReason *string                        `json:"finish_reason"`
}

type openRouter_Chunk_Error struct {
	Code     int            `json:"code"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata"`
}

type openRouter_Chunk_PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}
type openRouter_Chunk_Usage struct {
	CompletionTokens    int                                  `json:"completion_tokens"`
	Cost                float64                              `json:"cost"`
	PromptTokens        int                                  `json:"prompt_tokens"`
	PromptTokensDetails openRouter_Chunk_PromptTokensDetails `json:"prompt_tokens_details"`
	TotalTokens         int                                  `json:"total_tokens"`
}

type openRouter_Chunk struct {
	Choices []openRouter_Chunk_Choice `json:"choices"`
	Created int                       `json:"created"`
	Error   *openRouter_Chunk_Error   `json:"error"`
	ID      string                    `json:"id"`
	Model   string                    `json:"model"`
	Object  string                    `json:"object"`
	Usage   *openRouter_Chunk_Usage   `json:"usage"`
}
