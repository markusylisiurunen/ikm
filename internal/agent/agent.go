package agent

import (
	"context"
	"fmt"
	"sync"

	"slices"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
)

type Event any

type ChangeEvent struct{}

type ErrorEvent struct {
	Err error
}

type Agent struct {
	mux           sync.RWMutex
	logger        logger.Logger
	tools         []llm.Tool
	model         llm.Model
	system        func() string
	streamOptions []llm.StreamOption

	running  bool
	messages []llm.Message
	usage    llm.Usage

	subscriptions []chan<- Event
}

func New(logger logger.Logger, tools []llm.Tool) *Agent {
	return &Agent{logger: logger, tools: tools}
}

func (a *Agent) Reset() {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.running = false
	a.messages = nil
	a.usage = llm.Usage{}
}

func (a *Agent) Subscribe() (<-chan Event, func()) {
	subscription := make(chan Event)
	a.mux.Lock()
	a.subscriptions = append(a.subscriptions, subscription)
	a.mux.Unlock()
	return subscription, func() {
		a.mux.Lock()
		defer a.mux.Unlock()
		for i, sub := range a.subscriptions {
			if sub == subscription {
				a.subscriptions = slices.Delete(a.subscriptions, i, i+1)
				break
			}
		}
		close(subscription)
	}
}

func (a *Agent) SetModel(model llm.Model, options ...llm.StreamOption) {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.model = model
	a.streamOptions = options
	a.streamOptions = append(a.streamOptions, llm.WithMaxTurns(16))
}

func (a *Agent) SetSystem(system func() string) {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.system = system
}

func (a *Agent) GetIsRunning() bool {
	a.mux.RLock()
	defer a.mux.RUnlock()
	return a.running
}

func (a *Agent) GetHistoryState() ([]llm.Message, llm.Usage) {
	a.mux.RLock()
	defer a.mux.RUnlock()
	return a.messages, a.usage
}

func (a *Agent) Send(ctx context.Context, message string) {
	go a.send(ctx, message)
}
func (a *Agent) send(ctx context.Context, message string) {
	a.mux.Lock()
	if a.running {
		a.mux.Unlock()
		return
	}
	a.running = true
	a.mux.Unlock()
	a.messages = append(a.messages, llm.Message{
		Role:    llm.RoleUser,
		Content: llm.ContentParts{llm.NewTextContentPart(message)},
	})
	a.notify(&ChangeEvent{})
	for event := range a.model.Stream(ctx, a.getMessageHistory(), a.streamOptions...) {
		switch e := event.(type) {
		case *llm.ContentDeltaEvent:
			a.mux.Lock()
			if msg := a.messages[len(a.messages)-1]; msg.Role != llm.RoleAssistant {
				a.messages = append(a.messages, llm.Message{
					Role:    llm.RoleAssistant,
					Content: llm.ContentParts{},
				})
			}
			a.messages[len(a.messages)-1].Content.AppendText(e.Content)
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *llm.ToolUseEvent:
			a.mux.Lock()
			if msg := a.messages[len(a.messages)-1]; msg.Role != llm.RoleAssistant {
				a.messages = append(a.messages, llm.Message{
					Role:    llm.RoleAssistant,
					Content: llm.ContentParts{},
				})
			}
			a.messages[len(a.messages)-1].ToolCalls = append(
				a.messages[len(a.messages)-1].ToolCalls,
				llm.ToolCall{
					ID:    e.ID,
					Index: e.Index,
					Function: llm.ToolCallFunction{
						Name: e.FuncName,
						Args: e.FuncArgs,
					},
				},
			)
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *llm.ToolResultEvent:
			a.mux.Lock()
			var msg *llm.Message
			for i := len(a.messages) - 1; i >= 0; i-- {
				if a.messages[i].Role == llm.RoleAssistant {
					msg = &a.messages[i]
					break
				}
			}
			if msg == nil {
				a.logger.Error("tool result event without assistant message: %s", e.ID)
			} else {
				var toolCall *llm.ToolCall
				for _, call := range msg.ToolCalls {
					if call.ID == e.ID {
						toolCall = &call
						break
					}
				}
				if toolCall == nil {
					a.logger.Error("tool result event without matching tool call: %s", e.ID)
				} else {
					a.messages = append(a.messages, llm.Message{
						Role:       llm.RoleTool,
						ToolCallID: toolCall.ID,
						Name:       toolCall.Function.Name,
						Content:    llm.ContentParts{},
					})
					a.messages[len(a.messages)-1].Content.AppendText(e.Result)
				}
			}
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *llm.UsageEvent:
			a.mux.Lock()
			a.usage.PromptTokens = e.Usage.PromptTokens
			a.usage.CompletionTokens = e.Usage.CompletionTokens
			a.usage.TotalCost += e.Usage.TotalCost
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *llm.ErrorEvent:
			a.notify(&ErrorEvent{Err: e.Err})
		default:
			a.notify(fmt.Errorf("unknown event type: %T", e))
		}
	}
	a.mux.Lock()
	a.running = false
	a.mux.Unlock()
}

func (a *Agent) notify(event Event) {
	var subsToNotify []chan<- Event
	a.mux.RLock()
	if len(a.subscriptions) > 0 {
		subsToNotify = make([]chan<- Event, len(a.subscriptions))
		copy(subsToNotify, a.subscriptions)
	}
	a.mux.RUnlock()
	for _, ch := range subsToNotify {
		func() {
			defer func() {
				if r := recover(); r != nil {
					a.logger.Error("panic in agent notify: %v", r)
				}
			}()
			ch <- event
		}()
	}
}

func (a *Agent) getMessageHistory() []llm.Message {
	a.mux.RLock()
	defer a.mux.RUnlock()
	messages := make([]llm.Message, 0, 1+len(a.messages))
	if a.system != nil {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: llm.ContentParts{llm.NewTextContentPart(a.system())},
		})
	}
	return append(messages, a.messages...)
}
