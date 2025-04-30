package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/markusylisiurunen/ikm/internal/agent"
	"github.com/markusylisiurunen/ikm/internal/model"
)

type renderMsg struct{}

var renderCmd = tea.Cmd(func() tea.Msg {
	time.Sleep(250 * time.Millisecond)
	return renderMsg{}
})

type Model struct {
	agent     *agent.Agent
	viewport  viewport.Model
	textinput textinput.Model

	subscription <-chan agent.Event
	unsubscribe  func()

	cycles int
}

func Initial(llm model.Model) Model {
	m := Model{}
	m.agent = agent.New([]model.Tool{})
	m.agent.SetModel(llm, model.WithMaxTokens(32768), model.WithReasoningEffortHigh())
	m.subscription, m.unsubscribe = m.agent.Subscribe()
	go func() {
		for range m.subscription {
		}
	}()
	vp := viewport.New(0, 0)
	vp.KeyMap.Up.SetKeys("up")
	vp.KeyMap.Down.SetKeys("down")
	vp.KeyMap.PageUp.SetEnabled(false)
	vp.KeyMap.PageDown.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	m.viewport = vp
	ti := textinput.New()
	ti.Prompt = "\u276F "
	ti.Placeholder = "ask anything"
	ti.Focus()
	ti.CharLimit = 1024
	m.textinput = ti
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(renderMsg); ok {
		// TODO: re-render the viewport
		m.cycles++
		return m, renderCmd
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter {
			m.agent.Send(context.Background(), m.textinput.Value())
			m.textinput.Reset()
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 5
		m.textinput.Width = msg.Width - 3
		return m, nil
	}
	var cmd1, cmd2 tea.Cmd
	m.viewport, cmd1 = m.viewport.Update(msg)
	m.textinput, cmd2 = m.textinput.Update(msg)
	return m, tea.Batch(cmd1, cmd2)
}

func (m Model) View() string {
	var s string
	messages, _ := m.agent.GetState()
	for _, msg := range messages {
		if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
			s += msg.Content.Text() + "\n\n"
		}
	}
	return s + m.textinput.View()
}
