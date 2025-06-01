package llm

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

	"github.com/markusylisiurunen/ikm/internal/logger"
	"golang.org/x/sync/errgroup"
)

var _ Model = (*OpenRouter)(nil)

// ProviderConfig allows limiting OpenRouter requests to specific providers
type ProviderConfig struct {
	// Only restricts requests to specific providers (e.g., ["anthropic", "openai"])
	Only []string
	// Order specifies the preferred order of providers to try
	Order []string
	// AllowFallbacks determines if other providers can be used when Order is specified
	// If false, only providers in Order will be used
	AllowFallbacks *bool
}

type OpenRouter struct {
	logger   logger.Logger
	token    string
	model    string
	tools    []Tool
	provider *openRouter_Request_Provider
}

func NewOpenRouter(logger logger.Logger, token, model string) *OpenRouter {
	return &OpenRouter{logger: logger, token: token, model: model}
}

// NewOpenRouterWithProvider creates a new OpenRouter instance with provider configuration
func NewOpenRouterWithProvider(logger logger.Logger, token, model string, provider *ProviderConfig) *OpenRouter {
	o := &OpenRouter{logger: logger, token: token, model: model}
	if provider != nil {
		o.provider = &openRouter_Request_Provider{
			Only:          provider.Only,
			Order:         provider.Order,
			AllowFallback: provider.AllowFallbacks,
		}
	}
	return o
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
			select {
			case <-ctx.Done():
				ch <- &ErrorEvent{Err: ctx.Err()}
				return
			default:
			}
			out := tee(o.streamTurn(ctx, cloned, config), ch)
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
						for _, t := range o.tools {
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
func (o *OpenRouter) streamTurn(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		resp, err := o.request(ctx, messages, config)
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
				ch <- &ErrorEvent{Err: fmt.Errorf("non-ok status (%d) from OpenRouter: %s", resp.StatusCode, string(body))}
			}
			return
		}
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
		Provider:    o.provider,
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
	o.logger.Debug("OpenRouter request payload: %s", data.String())
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

type openRouter_Message_ContentPart_ImageURL struct {
	URL string `json:"url,omitzero"`
}

func (i openRouter_Message_ContentPart_ImageURL) IsZero() bool {
	return i.URL == ""
}

type openRouter_Message_ContentPart_File struct {
	FileName string `json:"filename,omitzero"`
	FileData string `json:"file_data,omitzero"`
}

func (f openRouter_Message_ContentPart_File) IsZero() bool {
	return f.FileName == "" && f.FileData == ""
}

type openRouter_Message_ContentPart struct {
	Type     string                                  `json:"type"`
	Text     string                                  `json:"text,omitzero"`
	ImageURL openRouter_Message_ContentPart_ImageURL `json:"image_url,omitzero"`
	File     openRouter_Message_ContentPart_File     `json:"file,omitzero"`
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

func (c *openRouter_Message_ContentParts) appendImage(urlOrBase64Data string) {
	if c == nil {
		return
	}
	*c = append(*c, openRouter_Message_ContentPart{
		Type: "image_url",
		ImageURL: openRouter_Message_ContentPart_ImageURL{
			URL: urlOrBase64Data,
		},
	})
}

func (c *openRouter_Message_ContentParts) appendFile(name, base64Data string) {
	if c == nil {
		return
	}
	*c = append(*c, openRouter_Message_ContentPart{
		Type: "file",
		File: openRouter_Message_ContentPart_File{
			FileName: name,
			FileData: base64Data,
		},
	})
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
		case ImageContentPart:
			if msg.Role != RoleUser {
				return fmt.Errorf("image content part can only be used in user messages, got role: %s", msg.Role)
			} else {
				m.ContentParts.appendImage(p.ImageURL)
			}
		case FileContentPart:
			if msg.Role != RoleUser {
				return fmt.Errorf("file content part can only be used in user messages, got role: %s", msg.Role)
			} else {
				m.ContentParts.appendFile(p.FileName, p.FileData)
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
		switch aux.Content[0] {
		case '"':
			var contentStr string
			if err := json.Unmarshal(aux.Content, &contentStr); err != nil {
				return fmt.Errorf("error unmarshalling OpenRouter message content as string: %w", err)
			}
			m.ContentParts = nil
			m.ContentString = contentStr
		case '[':
			var contentParts openRouter_Message_ContentParts
			if err := json.Unmarshal(aux.Content, &contentParts); err != nil {
				return fmt.Errorf("error unmarshalling OpenRouter message content as array: %w", err)
			}
			m.ContentParts = contentParts
			m.ContentString = ""
		default:
			return fmt.Errorf("unexpected content type in OpenRouter message: %s", aux.Content)
		}
	}
	return nil
}

func (m openRouter_Message) MarshalJSON() ([]byte, error) {
	type Alias openRouter_Message
	aux := &struct {
		Content any `json:"content,omitzero"`
		*Alias
	}{
		Alias: (*Alias)(&m),
	}
	if len(m.ContentParts) > 0 {
		aux.Content = m.ContentParts
	} else if m.ContentString != "" {
		aux.Content = m.ContentString
	} else if m.Role == string(RoleAssistant) {
		aux.Content = ""
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

type openRouter_Request_Provider struct {
	Only          []string `json:"only,omitempty"`
	Order         []string `json:"order,omitempty"`
	AllowFallback *bool    `json:"allow_fallbacks,omitempty"`
}

type openRouter_Request struct {
	MaxTokens   int                           `json:"max_tokens"`
	Messages    []openRouter_Message          `json:"messages"`
	Model       string                        `json:"model"`
	Provider    *openRouter_Request_Provider  `json:"provider,omitempty"`
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
