package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"golang.org/x/sync/errgroup"
)

var _ Model = (*OpenAI)(nil)

type OpenAIOption func(*OpenAI)

type OpenAI struct {
	logger logger.Logger
	token  string
	user   string
	model  string
	tools  []Tool
	usage  *openai_Usage
}

func NewOpenAI(logger logger.Logger, token, model string, opts ...OpenAIOption) *OpenAI {
	o := &OpenAI{
		logger: logger,
		token:  token,
		user:   fmt.Sprintf("%d", time.Now().Unix()),
		model:  model,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *OpenAI) Register(tool Tool) {
	if tool != nil {
		o.tools = append(o.tools, tool)
	}
}

func (o *OpenAI) Stream(ctx context.Context, messages []Message, opts ...StreamOption) <-chan Event {
	config := o.generationConfig(opts...)
	return o.streamTurns(ctx, messages, config)
}
func (o *OpenAI) streamTurns(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
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
func (o *OpenAI) streamTurn(ctx context.Context, messages []Message, config streamConfig) <-chan Event {
	o.usage = nil
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
				ch <- &ErrorEvent{Err: fmt.Errorf("non-ok status (%d) from OpenAI: %s", resp.StatusCode, string(body))}
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
					o.processSSEEvent(currentEvent, currentData, ch, toolCallBuffer)
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
			o.processSSEEvent(currentEvent, currentData, ch, toolCallBuffer)
		}
	}()
	return ch
}

func (o *OpenAI) request(ctx context.Context, messages []Message, config streamConfig) (*http.Response, error) {
	payload := openai_Request{
		Include:         []string{"reasoning.encrypted_content"},
		Input:           []openai_Message{},
		Instructions:    "",
		MaxOutputTokens: config.maxTokens,
		Model:           o.model,
		Reasoning:       nil,
		Store:           false,
		Stream:          true,
		Temperature:     config.temperature,
		Tools:           nil,
		User:            o.user,
	}
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			payload.Instructions = msg.Content.Text()
			continue
		}
		msgCopy := msg
		msgCopyToolCalls := make([]ToolCall, len(msg.ToolCalls))
		copy(msgCopyToolCalls, msg.ToolCalls)
		msgCopy.ToolCalls = nil
		// inject the original message
		if msgCopy.Content.Text() != "" {
			var rootInput openai_Message
			if err := rootInput.from(msgCopy); err != nil {
				return nil, fmt.Errorf("error converting message: %w", err)
			}
			payload.Input = append(payload.Input, rootInput)
		}
		// inject the potential tool calls
		for _, toolCall := range msgCopyToolCalls {
			msgCopy.ToolCalls = []ToolCall{toolCall}
			var toolInput openai_Message
			if err := toolInput.from(msgCopy); err != nil {
				return nil, fmt.Errorf("error converting tool call message: %w", err)
			}
			payload.Input = append(payload.Input, toolInput)
		}
	}
	if config.reasoningEffort > 0 {
		switch config.reasoningEffort {
		case 1:
			payload.Reasoning = &openai_Request_Reasoning{Effort: "low"}
		case 2:
			payload.Reasoning = &openai_Request_Reasoning{Effort: "medium"}
		case 3:
			payload.Reasoning = &openai_Request_Reasoning{Effort: "high"}
		default:
			o.logger.Errorf("invalid reasoning effort: %d, must be 1, 2, or 3", config.reasoningEffort)
		}
	} else if config.reasoningMaxTokens > 0 {
		return nil, fmt.Errorf("reasoningMaxTokens is not supported by OpenAI, use reasoningEffort instead")
	}
	if len(o.tools) > 0 {
		payload.Tools = make([]openai_Request_Tool, len(o.tools))
		for i, tool := range o.tools {
			name, description, parameters := tool.Spec()
			payload.Tools[i] = openai_Request_Tool{
				Type:        "function",
				Name:        name,
				Description: description,
				Parameters:  parameters,
			}
		}
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}
	o.logger.Debugj("OpenAI request payload", data.Bytes())
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, "https://api.openai.com/v1/responses", &data)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+o.token)
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 300 * time.Second /* 5 min */}
	return client.Do(req)
}

func (o *OpenAI) processSSEEvent(event, data string, ch chan<- Event, toolCallBuffer []*ToolUseEvent) {
	o.logger.Debugj(event, json.RawMessage(data))
	switch event {
	case "response.output_item.done":
		var outputItemDone openai_Response_OutputItemDone
		if err := json.Unmarshal([]byte(data), &outputItemDone); err != nil {
			o.logger.Errorf("failed to parse 'response.output_item.done': %v", err)
			return
		}
		if outputItemDone.Item.Type == "reasoning" {
			ch <- &ThinkingDeltaEvent{
				Thinking: outputItemDone.Item.EncryptedContent,
			}
			return
		}
		if outputItemDone.Item.Type == "function_call" {
			for i := range toolCallBuffer {
				if toolCallBuffer[i] == nil {
					toolCallBuffer[i] = &ToolUseEvent{
						ID:       outputItemDone.Item.CallID,
						Index:    i,
						FuncName: outputItemDone.Item.Name,
						FuncArgs: outputItemDone.Item.Arguments,
					}
					break
				}
			}
			return
		}
	case "response.output_text.delta":
		var outputTextDelta openai_Response_OutputTextDelta
		if err := json.Unmarshal([]byte(data), &outputTextDelta); err != nil {
			o.logger.Errorf("failed to parse 'response.output_text.delta': %v", err)
			return
		}
		if outputTextDelta.Delta != "" {
			ch <- &ContentDeltaEvent{
				Content: outputTextDelta.Delta,
			}
			return
		}
	case "response.completed":
		var responseCompleted openai_Response_ResponseCompleted
		if err := json.Unmarshal([]byte(data), &responseCompleted); err != nil {
			o.logger.Errorf("failed to parse 'response.completed': %v", err)
			return
		}
		for _, toolCall := range toolCallBuffer {
			if toolCall == nil {
				break
			}
			ch <- toolCall
		}
		if responseCompleted.Response.Usage != nil {
			ch <- &UsageEvent{
				Usage: Usage{
					PromptTokens:     responseCompleted.Response.Usage.InputTokens,
					CompletionTokens: responseCompleted.Response.Usage.OutputTokens,
					TotalCost:        o.estimateCost(*responseCompleted.Response.Usage),
				},
			}
		}
	}
}

func (o *OpenAI) generationConfig(opts ...StreamOption) streamConfig {
	c := streamConfig{
		maxTokens:          8192,
		maxTurns:           1,
		reasoningEffort:    0,
		reasoningMaxTokens: 0,
		temperature:        1.0,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return c
}

func (o *OpenAI) estimateCost(usage openai_Usage) float64 {
	type costConfig struct {
		inputTokens  float64
		cachedTokens float64
		outputTokens float64
	}
	costs := map[string]costConfig{
		"codex-mini-latest": {
			inputTokens:  1.5,
			cachedTokens: 0.375,
			outputTokens: 6.0,
		},
		"o3": {
			inputTokens:  2.0,
			cachedTokens: 0.5,
			outputTokens: 8.0,
		},
		"o4-mini": {
			inputTokens:  1.1,
			cachedTokens: 0.275,
			outputTokens: 4.4,
		},
	}
	var cost *costConfig
	if c, ok := costs[o.model]; ok {
		cost = &c
	} else {
		o.logger.Errorf("no cost information available for model %s, using intentionally high default values", o.model)
		cost = &costConfig{
			inputTokens:  10 * costs["o3"].inputTokens,
			cachedTokens: 10 * costs["o3"].cachedTokens,
			outputTokens: 10 * costs["o3"].outputTokens,
		}
	}
	millionInputTokens := float64(usage.InputTokens-usage.InputTokensDetails.CachedTokens) / 1000000.0
	millionCachedTokens := float64(usage.InputTokensDetails.CachedTokens) / 1000000.0
	millionOutputTokens := float64(usage.OutputTokens) / 1000000.0
	// compute the cost with and without cache
	costWithCache := millionInputTokens*cost.inputTokens +
		millionCachedTokens*cost.cachedTokens +
		millionOutputTokens*cost.outputTokens
	costWithoutCache := (millionInputTokens+millionCachedTokens)*cost.inputTokens +
		millionOutputTokens*cost.outputTokens
	o.logger.Debugf("OpenAI cost estimate: $%.6f (without cache), $%.6f (with cache), saved $%.6f or %.2f%%",
		costWithoutCache, costWithCache, costWithoutCache-costWithCache, (costWithoutCache-costWithCache)/costWithoutCache*100)
	return costWithCache
}

// helper types ------------------------------------------------------------------------------------

// messages
type openai_InputMessage_ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type openai_InputMessage struct {
	Role    string                            `json:"role"`
	Content []openai_InputMessage_ContentItem `json:"content"`
}

type openai_OutputMessage_ContentItem struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}
type openai_OutputMessage struct {
	ID      string                             `json:"id"`
	Type    string                             `json:"type"`
	Role    string                             `json:"role"`
	Status  string                             `json:"status"`
	Content []openai_OutputMessage_ContentItem `json:"content"`
}

type openai_FunctionToolCall struct {
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
}

type openai_FunctionToolCallOutput struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
	Type   string `json:"type"`
}

type openai_Message struct {
	v any
}

func (m openai_Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.v)
}

func (m *openai_Message) from(msg Message) error {
	if len(msg.ToolCalls) > 1 {
		return fmt.Errorf("OpenAI does not support multiple tool calls in a single message")
	}
	if len(msg.ToolCalls) > 0 {
		var v openai_FunctionToolCall
		v.Arguments = msg.ToolCalls[0].Function.Args
		v.CallID = msg.ToolCalls[0].ID
		v.Name = msg.ToolCalls[0].Function.Name
		v.Type = "function_call"
		m.v = v
		return nil
	}
	switch msg.Role {
	case RoleSystem:
		return fmt.Errorf("OpenAI does not support system messages")
	case RoleAssistant:
		var v openai_OutputMessage
		v.ID = generateOpenAIMsgID()
		v.Type = "message"
		v.Role = "assistant"
		v.Status = "completed"
		for _, part := range msg.Content {
			switch p := part.(type) {
			case TextContentPart:
				v.Content = append(v.Content, openai_OutputMessage_ContentItem{
					Type:        "output_text",
					Text:        p.Text,
					Annotations: []any{},
				})
			}
		}
		m.v = v
	case RoleUser:
		var v openai_InputMessage
		v.Role = "user"
		for _, part := range msg.Content {
			switch p := part.(type) {
			case TextContentPart:
				v.Content = append(v.Content, openai_InputMessage_ContentItem{
					Type: "input_text",
					Text: p.Text,
				})
			case ImageContentPart:
				return fmt.Errorf("image content part currently not supported in OpenAI messages")
			case FileContentPart:
				return fmt.Errorf("file content part currently not supported in OpenAI messages")
			}
		}
		m.v = v
	case RoleTool:
		var v openai_FunctionToolCallOutput
		v.CallID = msg.ToolCallID
		v.Output = msg.Content.Text()
		v.Type = "function_call_output"
		m.v = v
	default:
		return fmt.Errorf("unexpected message role: %s", msg.Role)
	}
	return nil
}

// requests
type openai_Request_Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type openai_Request_Reasoning struct {
	Effort string `json:"effort"`
}
type openai_Request struct {
	Include         []string                  `json:"include"`
	Input           []openai_Message          `json:"input"`
	Instructions    string                    `json:"instructions,omitzero"`
	MaxOutputTokens int                       `json:"max_output_tokens,omitzero"`
	Model           string                    `json:"model"`
	Reasoning       *openai_Request_Reasoning `json:"reasoning,omitzero"`
	Store           bool                      `json:"store"`
	Stream          bool                      `json:"stream"`
	Temperature     float64                   `json:"temperature"`
	Tools           []openai_Request_Tool     `json:"tools,omitzero"`
	User            string                    `json:"user,omitzero"`
}

// responses
type openai_Usage_InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}
type openai_Usage_OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}
type openai_Usage struct {
	InputTokens         int                               `json:"input_tokens"`
	InputTokensDetails  *openai_Usage_InputTokensDetails  `json:"input_tokens_details"`
	OutputTokens        int                               `json:"output_tokens"`
	OutputTokensDetails *openai_Usage_OutputTokensDetails `json:"output_tokens_details"`
	TotalTokens         int                               `json:"total_tokens"`
}

type openai_Response_OutputItemDone struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	OutputIndex    int    `json:"output_index"`
	Item           struct {
		Arguments        string   `json:"arguments"`
		CallID           string   `json:"call_id"`
		Content          []any    `json:"content"`
		EncryptedContent string   `json:"encrypted_content"`
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Role             string   `json:"role"`
		Status           string   `json:"status"`
		Summary          []string `json:"summary"`
		Type             string   `json:"type"`
	} `json:"item"`
}

type openai_Response_OutputTextDelta struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
	ItemID         string `json:"item_id"`
}

type openai_Response_ResponseCompleted struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	Response       struct {
		Usage *openai_Usage `json:"usage"`
	}
}

// utility functions -------------------------------------------------------------------------------

func generateOpenAIMsgID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const charset = "0123456789abcdef"
	result := make([]byte, 48)
	for i := range result {
		result[i] = charset[r.Intn(len(charset))]
	}
	return "msg_" + string(result)
}
