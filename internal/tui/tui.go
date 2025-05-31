package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/fatih/color"
	"github.com/markusylisiurunen/ikm/internal/agent"
	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/markusylisiurunen/ikm/toolkit/tool"
)

type agentMsg struct {
	err  error
	done bool
}

func waitAgentCmd(subscription <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-subscription
		if !ok {
			return agentMsg{done: true}
		}
		switch event := event.(type) {
		case *agent.ErrorEvent:
			return agentMsg{err: event.Err}
		default:
			return agentMsg{}
		}
	}
}

type model_Mode struct {
	name   string
	system func() string
}

type Model struct {
	logger          logger.Logger
	runInBashDocker func(context.Context, string) (int, string, string, error)

	openRouterKey   string
	openRouterModel string

	fastButCapableModel    string
	thoroughButCostlyModel string

	viewport  viewport.Model
	textinput textinput.Model

	mode         model_Mode
	modes        []model_Mode
	agent        *agent.Agent
	subscription <-chan agent.Event
	unsubscribe  func()

	cancelFunc context.CancelFunc
}

type modelOption func(*Model)

func WithStaticMode(name string, system string) modelOption {
	return func(m *Model) {
		m.modes = append(m.modes, model_Mode{
			name:   name,
			system: func() string { return system },
		})
	}
}

func WithDynamicMode(name string, system func() string) modelOption {
	return func(m *Model) {
		m.modes = append(m.modes, model_Mode{
			name:   name,
			system: system,
		})
	}
}

func WithSetDefaultMode(name string) modelOption {
	return func(m *Model) {
		for _, mode := range m.modes {
			if mode.name == name {
				m.mode = mode
				return
			}
		}
	}
}

func Initial(
	logger logger.Logger,
	openRouterKey string,
	runInBashDocker func(context.Context, string) (int, string, string, error),
	opts ...modelOption,
) Model {
	m := Model{
		logger:          logger,
		runInBashDocker: runInBashDocker,
		openRouterKey:   openRouterKey,
	}
	for _, opt := range opts {
		opt(&m)
	}
	if m.mode.name == "" || len(m.modes) == 0 {
		panic("no modes defined or default mode not set")
	}
	// init the model
	models := m.listModels()
	m.openRouterModel = models[0]
	m.fastButCapableModel = "google/gemini-2.5-flash-preview-05-20"
	m.thoroughButCostlyModel = "openai/gpt-4.1"
	// init the agent
	m.agent = agent.New(logger, []llm.Tool{})
	model := llm.NewOpenRouter(logger, m.openRouterKey, m.openRouterModel)
	model.Register(tool.NewBash(m.runInBashDocker).SetLogger(logger))
	model.Register(tool.NewFS().SetLogger(logger))
	model.Register(tool.NewLLM(m.openRouterKey).SetLogger(logger))
	model.Register(tool.NewTask(m.openRouterKey, m.fastButCapableModel, m.thoroughButCostlyModel).SetLogger(logger))
	m.agent.SetModel(model, llm.WithMaxTokens(32768), llm.WithReasoningEffortHigh())
	m.agent.SetSystem(m.mode.system)
	m.subscription, m.unsubscribe = m.agent.Subscribe()
	// init the viewport
	vp := viewport.New(0, 0)
	vp.KeyMap.Up.SetKeys("up")
	vp.KeyMap.Down.SetKeys("down")
	vp.KeyMap.PageUp.SetEnabled(false)
	vp.KeyMap.PageDown.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	m.viewport = vp
	// init the textinput
	ti := textinput.New()
	ti.Prompt = "\u276F "
	ti.Placeholder = "ask anything"
	ti.Focus()
	ti.CharLimit = 1024
	m.textinput = ti
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitAgentCmd(m.subscription))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(agentMsg); ok {
		if msg.done {
			return m, nil
		}
		if msg.err != nil {
			if !errors.Is(msg.err, context.Canceled) {
				m.logger.Error(msg.err.Error())
			}
			return m, waitAgentCmd(m.subscription)
		}
		m.viewport.SetContent(m.renderContent())
		return m, waitAgentCmd(m.subscription)
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.unsubscribe != nil {
				m.unsubscribe()
			}
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEsc {
			if m.agent.GetIsRunning() && m.cancelFunc != nil {
				m.cancelFunc()
				m.cancelFunc = nil
				return m, nil
			}
		}
		if msg.Type == tea.KeyEnter {
			if strings.HasPrefix(m.textinput.Value(), "/") {
				m.handleSlashCommand()
				return m, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelFunc = cancel
			m.agent.Send(ctx, m.textinput.Value())
			m.textinput.Reset()
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 4
		m.viewport.SetContent(m.renderContent())
		if m.viewport.PastBottom() {
			m.viewport.GotoBottom()
		}
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
	s += m.viewport.View()
	s += "\n\n" + m.textinput.View()
	s += "\n\n" + color.New(color.Faint).Sprint(m.renderFooter())
	return s
}

func (m Model) renderContent() string {
	var s string
	messages, _ := m.agent.GetHistoryState()
	for i, msg := range messages {
		if msg.Role == llm.RoleUser {
			if i > 0 {
				s += "\n\n"
			}
			content := wrapWithPrefix("\u203A "+msg.Content.Text(), "", m.viewport.Width)
			s += color.New(color.Faint).Sprint(strings.TrimSpace(content))
		}
		if msg.Role == llm.RoleAssistant {
			if i > 0 {
				s += "\n\n"
			}
			content := msg.Content.Text()
			if content != "" {
				s += m.renderMarkdown(content)
			}
			for idx, call := range msg.ToolCalls {
				if content != "" || idx > 0 {
					s += "\n\n"
				}
				s += color.New(color.FgYellow).Sprint("\u25CF") + color.New(color.Bold).Sprintf(" %s", call.Function.Name)
			}
		}
	}
	return s
}

func (m Model) renderMarkdown(content string) string {
	var margin uint = 0
	dark := styles.DarkStyleConfig
	dark.Document.Color = nil
	dark.Document.Margin = &margin
	dark.H1 = dark.H2
	dark.H1.Prefix = "# "
	dark.Code.Prefix = ""
	dark.Code.Suffix = ""
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStyles(dark),
		glamour.WithWordWrap(m.viewport.Width),
	)
	markdown, _ := renderer.Render(strings.TrimSpace(content))
	return strings.TrimSpace(markdown)
}

func (m Model) renderFooter() string {
	if value := m.textinput.Value(); strings.HasPrefix(value, "/") {
		for _, cmd := range m.listSlashCommands() {
			if value == "/"+cmd || strings.HasPrefix(value, "/"+cmd+" ") {
				args := strings.Fields(value)
				if len(args) <= 1 {
					args = []string{}
				} else {
					args = args[1:]
				}
				return m.getSlashCommandHelp(cmd, args)
			}
		}
		return strings.Join(m.listSlashCommands(), ", ")
	}
	isRunning := m.agent.GetIsRunning()
	_, usage := m.agent.GetHistoryState()
	var meta string
	meta += fmt.Sprintf("%s, ", m.mode.name)
	meta += fmt.Sprintf("%s, ", m.getModelSlug(m.openRouterModel))
	meta += fmt.Sprintf("cost: %.3f €, ", usage.TotalCost)
	meta += fmt.Sprintf("tokens: %d", usage.PromptTokens+usage.CompletionTokens)
	if isRunning {
		return "working... (" + meta + ")"
	}
	return "ctrl+c to quit. (" + meta + ")"
}

// available models --------------------------------------------------------------------------------

func (m Model) listModels() []string {
	return []string{
		"anthropic/claude-3.7-sonnet",
		"google/gemini-2.5-flash-preview",
		"google/gemini-2.5-flash-preview:thinking",
		"google/gemini-2.5-pro-preview",
		"openai/gpt-4.1",
		"openai/gpt-4.1-mini",
		"openai/o3",
		"openai/o4-mini-high",
	}
}

func (m Model) getModelSlug(model string) string {
	switch model {
	case "anthropic/claude-3.7-sonnet":
		return "claude-3.7-sonnet"
	case "google/gemini-2.5-flash-preview":
		return "gemini-2.5-flash"
	case "google/gemini-2.5-flash-preview:thinking":
		return "gemini-2.5-flash-thinking"
	case "google/gemini-2.5-pro-preview":
		return "gemini-2.5-pro"
	case "openai/gpt-4.1":
		return "gpt-4.1"
	case "openai/gpt-4.1-mini":
		return "gpt-4.1-mini"
	case "openai/o3":
		return "o3"
	case "openai/o4-mini-high":
		return "o4-mini-high"
	default:
		return ""
	}
}

// slash commands ----------------------------------------------------------------------------------

func (m Model) listSlashCommands() []string {
	return []string{
		"clear",
		"copy",
		"mode",
		"model",
	}
}

func (m Model) getSlashCommandHelp(cmd string, args []string) string {
	switch cmd {
	case "clear":
		return "clears the conversation history."
	case "copy":
		return "copies the last assistant message to the clipboard."
	case "mode":
		names := make([]string, len(m.modes))
		for i, mode := range m.modes {
			names[i] = mode.name
		}
		return fmt.Sprintf("sets the mode to %s.", strings.Join(names, ", "))
	case "model":
		var slugs []string
		for _, id := range m.listModels() {
			slug := m.getModelSlug(id)
			if len(args) > 0 && !strings.HasPrefix(slug, args[0]) {
				continue
			}
			slugs = append(slugs, slug)
		}
		return strings.Join(slugs, ", ")
	default:
		return ""
	}
}

func (m *Model) handleSlashCommand() {
	defer m.textinput.Reset()
	fields := strings.Fields(m.textinput.Value())
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "/clear":
		m.handleClearSlashCommand()
	case "/copy":
		m.handleCopySlashCommand()
	case "/mode":
		m.handleModeSlashCommand(fields[1:])
	case "/model":
		m.handleModelSlashCommand(fields[1:])
	}
}

func (m *Model) handleClearSlashCommand() {
	m.agent.Reset()
}

func (m *Model) handleCopySlashCommand() {
	messages, _ := m.agent.GetHistoryState()
	var content string
	for _, i := range slices.Backward(messages) {
		if i.Role == llm.RoleAssistant {
			content = i.Content.Text()
			break
		}
	}
	if content == "" {
		return
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		m.logger.Error("failed to copy to clipboard: %v", err)
	}
}

func (m *Model) handleModeSlashCommand(args []string) {
	for _, mode := range m.modes {
		if mode.name == args[0] {
			m.mode = mode
			m.agent.SetSystem(mode.system)
			return
		}
	}
}

func (m *Model) handleModelSlashCommand(args []string) {
	if len(args) == 0 {
		return
	}
	for _, id := range m.listModels() {
		if m.getModelSlug(id) == args[0] {
			m.openRouterModel = id
			model := llm.NewOpenRouter(m.logger, m.openRouterKey, m.openRouterModel)
			model.Register(tool.NewBash(m.runInBashDocker).SetLogger(m.logger))
			model.Register(tool.NewFS().SetLogger(m.logger))
			model.Register(tool.NewLLM(m.openRouterKey).SetLogger(m.logger))
			model.Register(tool.NewTask(m.openRouterKey, m.fastButCapableModel, m.thoroughButCostlyModel).SetLogger(m.logger))
			m.agent.SetModel(model, llm.WithMaxTokens(32768), llm.WithReasoningEffortHigh())
			return
		}
	}
}
