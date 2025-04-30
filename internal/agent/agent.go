package agent

import (
	"context"
	"fmt"
	"sync"

	"slices"

	"github.com/markusylisiurunen/ikm/internal/model"
)

type Event any

type ChangeEvent struct{}

type ErrorEvent struct {
	Err error
}

type Agent struct {
	mux           sync.RWMutex
	tools         []model.Tool
	model         model.Model
	streamOptions []model.StreamOption

	running  bool
	messages []model.Message
	usage    model.Usage

	subscriptions []chan<- Event
}

func New(tools []model.Tool) *Agent {
	return &Agent{tools: tools}
}

func (a *Agent) Reset() {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.running = false
	a.messages = nil
	a.usage = model.Usage{}
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

func (a *Agent) SetModel(llm model.Model, options ...model.StreamOption) {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.model = llm
	a.streamOptions = options
	a.streamOptions = append(a.streamOptions, model.WithMaxTurns(16))
}

func (a *Agent) GetState() ([]model.Message, model.Usage) {
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
	a.messages = append(a.messages, model.Message{
		Role:    model.RoleUser,
		Content: model.ContentParts{model.NewTextContentPart(message)},
	})
	for event := range a.model.Stream(ctx, a.getMessageHistory(), a.streamOptions...) {
		switch e := event.(type) {
		case *model.ContentDeltaEvent:
			a.mux.Lock()
			if msg := a.messages[len(a.messages)-1]; msg.Role != model.RoleAssistant {
				a.messages = append(a.messages, model.Message{
					Role:    model.RoleAssistant,
					Content: model.ContentParts{},
				})
			}
			a.messages[len(a.messages)-1].Content.AppendText(e.Content)
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *model.ToolUseEvent:
			continue // TODO: handle tool use
		case *model.ToolResultEvent:
			continue // TODO: handle tool result
		case *model.UsageEvent:
			a.mux.Lock()
			a.usage.PromptTokens += e.Usage.PromptTokens
			a.usage.CompletionTokens += e.Usage.CompletionTokens
			a.usage.TotalCost += e.Usage.TotalCost
			a.mux.Unlock()
			a.notify(&ChangeEvent{})
		case *model.ErrorEvent:
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
	a.mux.RLock()
	defer a.mux.RUnlock()
	for _, ch := range a.subscriptions {
		ch <- event
	}
}

func (a *Agent) getMessageHistory() []model.Message {
	a.mux.RLock()
	defer a.mux.RUnlock()
	messages := make([]model.Message, 0, 1+len(a.messages))
	messages = append(messages, model.Message{
		Role:    model.RoleSystem,
		Content: model.ContentParts{model.NewTextContentPart("You are a helpful assistant.")},
	})
	return append(messages, a.messages...)
}
