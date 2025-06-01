package llm

import (
	"context"
	"strings"
)

// events ------------------------------------------------------------------------------------------

type Event any

type ContentDeltaEvent struct {
	Content string
}

type ToolUseEvent struct {
	ID       string
	Index    int
	FuncName string
	FuncArgs string
}

type ToolResultEvent struct {
	ID     string
	Result string
	Error  error
}

type UsageEvent struct {
	Usage Usage
}

type ErrorEvent struct {
	Err error
}

// messages ----------------------------------------------------------------------------------------

type Role string

const (
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleUser      Role = "user"
)

type ToolCallFunction struct {
	Name string
	Args string
}
type ToolCall struct {
	ID       string
	Index    int
	Function ToolCallFunction
}

type ContentPart any

type TextContentPart struct {
	Type string
	Text string
}

func NewTextContentPart(text string) TextContentPart {
	return TextContentPart{Type: "text", Text: text}
}

type ImageContentPart struct {
	Type     string
	ImageURL string
}

func NewImageContentPart(urlOrBase64Data string) ImageContentPart {
	return ImageContentPart{
		Type:     "image_url",
		ImageURL: urlOrBase64Data,
	}
}

type FileContentPart struct {
	Type     string
	FileName string
	FileData string
}

func NewFileContentPart(name, base64Data string) FileContentPart {
	return FileContentPart{
		Type:     "file",
		FileName: name,
		FileData: base64Data,
	}
}

type ContentParts []ContentPart

func (c *ContentParts) AppendText(text string) {
	if c == nil {
		return
	}
	if len(*c) == 0 {
		*c = append(*c, NewTextContentPart(text))
	} else if p, ok := (*c)[len(*c)-1].(TextContentPart); ok {
		p.Text += text
		(*c)[len(*c)-1] = p
	} else {
		*c = append(*c, NewTextContentPart(text))
	}
}

func (c ContentParts) Text() string {
	var sb strings.Builder
	for _, part := range c {
		switch p := part.(type) {
		case TextContentPart:
			sb.WriteString(p.Text)
		case ImageContentPart:
			sb.WriteString("\n\n[image: " + p.ImageURL + "]\n\n")
		case FileContentPart:
			sb.WriteString("\n\n[file: " + p.FileName + "]\n\n")
		}
	}
	return sb.String()
}

type Message struct {
	Role       Role
	Content    ContentParts
	ToolCalls  []ToolCall
	Name       string
	ToolCallID string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalCost        float64
}

// model -------------------------------------------------------------------------------------------

type StopCondition func(turn int, history []Message) bool

type streamConfig struct {
	maxTokens       int
	maxTurns        int
	reasoningEffort uint8
	stopCondition   StopCondition
	temperature     float64
}

type StreamOption func(*streamConfig)

func WithMaxTokens(maxTokens int) StreamOption {
	return func(c *streamConfig) { c.maxTokens = maxTokens }
}
func WithMaxTurns(maxTurns int) StreamOption {
	return func(c *streamConfig) { c.maxTurns = maxTurns }
}
func WithReasoningEffortHigh() StreamOption {
	return func(c *streamConfig) { c.reasoningEffort = 3 }
}
func WithReasoningEffortMedium() StreamOption {
	return func(c *streamConfig) { c.reasoningEffort = 2 }
}
func WithReasoningEffortLow() StreamOption {
	return func(c *streamConfig) { c.reasoningEffort = 1 }
}
func WithTemperature(temperature float64) StreamOption {
	return func(c *streamConfig) { c.temperature = temperature }
}
func WithStopCondition(condition StopCondition) StreamOption {
	return func(c *streamConfig) { c.stopCondition = condition }
}

type Model interface {
	Register(tool Tool)
	Stream(ctx context.Context, messages []Message, opts ...StreamOption) <-chan Event
}
