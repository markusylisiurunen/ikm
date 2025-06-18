package llm

import "fmt"

type messageBuilder struct {
	init  bool
	msgs  []Message
	usage Usage
	err   error
}

func newMessageBuilder() *messageBuilder {
	return &messageBuilder{init: true, msgs: []Message{}, usage: Usage{}, err: nil}
}

func (b *messageBuilder) process(event Event) {
	if b.err != nil {
		return
	}
	switch e := event.(type) {
	case *ThinkingDeltaEvent:
		if b.init {
			b.init = false
			b.msgs = append(b.msgs, Message{Role: RoleAssistant, Content: ContentParts{}})
		}
		if b.msgs[len(b.msgs)-1].Role != RoleAssistant {
			return
		}
		if len(b.msgs[len(b.msgs)-1].Content) == 0 {
			b.msgs[len(b.msgs)-1].Content = append(b.msgs[len(b.msgs)-1].Content, NewThinkingContentPart(""))
		}
		if p, ok := b.msgs[len(b.msgs)-1].Content[0].(ThinkingContentPart); ok {
			// append to the last thinking content part
			p.Thinking += e.Thinking
			b.msgs[len(b.msgs)-1].Content[0] = p
		}
	case *ContentDeltaEvent:
		if b.init {
			b.init = false
			b.msgs = append(b.msgs, Message{Role: RoleAssistant, Content: ContentParts{}})
		}
		b.msgs[len(b.msgs)-1].Content.AppendText(e.Content)
	case *ToolUseEvent:
		if b.init {
			b.init = false
			b.msgs = append(b.msgs, Message{Role: RoleAssistant, Content: ContentParts{}})
		}
		tc := ToolCall{
			ID:    e.ID,
			Index: e.Index,
			Function: ToolCallFunction{
				Name: e.FuncName,
				Args: e.FuncArgs,
			},
		}
		b.msgs[len(b.msgs)-1].ToolCalls = append(b.msgs[len(b.msgs)-1].ToolCalls, tc)
	case *ToolResultEvent:
		b.init = true
		var msg *Message
		for i := len(b.msgs) - 1; i >= 0; i-- {
			if b.msgs[i].Role == RoleAssistant {
				msg = &b.msgs[i]
				break
			}
		}
		if msg == nil {
			b.err = fmt.Errorf("tool result event without assistant message: %s", e.ID)
			return
		} else {
			var toolCall *ToolCall
			for _, call := range msg.ToolCalls {
				if call.ID == e.ID {
					toolCall = &call
					break
				}
			}
			if toolCall == nil {
				b.err = fmt.Errorf("tool result event without matching tool call: %s", e.ID)
				return
			} else {
				b.msgs = append(b.msgs, Message{
					Role:       RoleTool,
					Content:    ContentParts{NewTextContentPart(e.Result)},
					Name:       toolCall.Function.Name,
					ToolCallID: e.ID,
				})
			}
		}
	case *UsageEvent:
		b.usage.PromptTokens += e.Usage.PromptTokens
		b.usage.CompletionTokens += e.Usage.CompletionTokens
		b.usage.TotalCost += e.Usage.TotalCost
	case *ErrorEvent:
		b.err = e.Err
	}
}

func (b *messageBuilder) result() ([]Message, Usage, error) {
	if b.err != nil {
		return nil, Usage{}, b.err
	}
	return b.msgs, b.usage, nil
}

func Rollup(events <-chan Event) ([]Message, Usage, error) {
	b := newMessageBuilder()
	for event := range events {
		b.process(event)
	}
	return b.result()
}

func tee(in <-chan Event, out chan<- Event) <-chan Event {
	fork := make(chan Event)
	go func() {
		defer close(fork)
		for event := range in {
			out <- event
			fork <- event
		}
	}()
	return fork
}
