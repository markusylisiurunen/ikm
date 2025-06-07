package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"slices"

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
	"github.com/tidwall/gjson"
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

	anthropicKey    string
	openRouterKey   string
	openRouterModel string

	fastButCapableModel    string
	thoroughButCostlyModel string

	viewport  viewport.Model
	textinput textinput.Model

	mode          model_Mode
	modes         []model_Mode
	disabledTools []string
	agent         *agent.Agent
	subscription  <-chan agent.Event
	unsubscribe   func()

	cancelFunc context.CancelFunc
	errorMsg   string
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

func WithSetDefaultModel(model string) modelOption {
	return func(m *Model) {
		for _, id := range m.listModels() {
			if m.getModelSlug(id) == model {
				m.openRouterModel = id
				return
			}
		}
	}
}

func WithDisabledTools(tools []string) modelOption {
	return func(m *Model) {
		m.disabledTools = tools
	}
}

func Initial(
	logger logger.Logger,
	anthropicKey string,
	openRouterKey string,
	runInBashDocker func(context.Context, string) (int, string, string, error),
	opts ...modelOption,
) Model {
	m := Model{
		logger:          logger,
		runInBashDocker: runInBashDocker,
		anthropicKey:    anthropicKey,
		openRouterKey:   openRouterKey,
	}
	for _, opt := range opts {
		opt(&m)
	}
	if m.mode.name == "" || len(m.modes) == 0 {
		panic("no modes defined or default mode not set")
	}
	// init the model
	if m.openRouterModel == "" {
		m.openRouterModel = m.listModels()[0]
	}
	m.fastButCapableModel = "google/gemini-2.5-flash-preview-05-20"
	m.thoroughButCostlyModel = "anthropic/claude-sonnet-4"
	// init the agent
	m.agent = agent.New(logger, []llm.Tool{})
	model := m.createModelInstance(m.openRouterModel)
	m.registerTools(model)
	m.configureAgentModel(m.openRouterModel, model)
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
	ti.CharLimit = 4096
	m.textinput = ti
	return m
}

func (m Model) registerTools(model llm.Model) {
	if !m.isToolDisabled("bash") {
		model.Register(tool.NewBash(m.runInBashDocker).SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: bash")
	}
	if !m.isToolDisabled("fs") {
		model.Register(tool.NewFSList().SetLogger(m.logger))
		model.Register(tool.NewFSRead().SetLogger(m.logger))
		model.Register(tool.NewFSReplace().SetLogger(m.logger))
		model.Register(tool.NewFSWrite().SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: fs")
	}
	if !m.isToolDisabled("llm") {
		model.Register(tool.NewLLM(m.openRouterKey).SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: llm")
	}
	if !m.isToolDisabled("task") {
		model.Register(tool.NewTask(
			m.runInBashDocker,
			m.openRouterKey,
			m.fastButCapableModel, m.thoroughButCostlyModel,
		).SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: task")
	}
	if !m.isToolDisabled("think") {
		model.Register(tool.NewThink().SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: think")
	}
	if !m.isToolDisabled("todo") {
		model.Register(tool.NewTodoRead().SetLogger(m.logger))
		model.Register(tool.NewTodoWrite().SetLogger(m.logger))
	} else {
		m.logger.Debug("skipped disabled tool: todo")
	}
}

func (m Model) isToolDisabled(toolName string) bool {
	return slices.Contains(m.disabledTools, toolName)
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
				m.errorMsg = msg.err.Error()
			}
			m.viewport.SetContent(m.renderContent())
			m.viewport.GotoBottom()
			return m, waitAgentCmd(m.subscription)
		}
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderContent())
		if atBottom {
			m.viewport.GotoBottom()
		}
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
			m.errorMsg = ""
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
				var circleColor *color.Color
				if m.agent.IsToolCallInFlight(call.ID) {
					circleColor = color.New(color.FgYellow)
				} else {
					circleColor = color.New(color.FgGreen)
				}
				s += circleColor.Sprint("\u25CF") + color.New(color.Bold).Sprintf(" %s", call.Function.Name)
				switch call.Function.Name {
				case "bash":
					s += m.renderToolBash(call.Function.Args)
				case "fs_list":
					s += m.renderToolFSList(call.Function.Args)
				case "fs_read":
					s += m.renderToolFSRead(call.Function.Args)
				case "fs_replace":
					s += m.renderToolFSReplace(call.Function.Args)
				case "fs_write":
					s += m.renderToolFSWrite(call.Function.Args)
				case "llm":
					s += m.renderToolLLM(call.Function.Args)
				case "task":
					s += m.renderToolTask(call.Function.Args)
				case "think":
					s += m.renderToolThink(call.Function.Args)
				case "todo_read":
					s += m.renderToolTodoRead(call.Function.Args)
				case "todo_write":
					s += m.renderToolTodoWrite(call.Function.Args)
				}
			}
		}
	}
	if m.errorMsg != "" {
		if s != "" {
			s += "\n\n"
		}
		s += m.renderError(m.errorMsg)
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

func (m Model) renderError(errorMsg string) string {
	const (
		borderBottomLeft  = "┗"
		borderBottomRight = "┛"
		borderHorizontal  = "━"
		borderTopLeft     = "┏"
		borderTopRight    = "┓"
		borderVertical    = "┃"
		padding           = 2
	)
	// calculate usable width
	maxContentWidth := max(m.viewport.Width-2*padding-2, 10)
	wrappedLines := strings.Split(wrapWithPrefix(errorMsg, "", maxContentWidth), "\n")
	var result strings.Builder
	boxWidth := m.viewport.Width - 2
	// top border
	result.WriteString(color.New(color.FgRed, color.Bold).Sprint(borderTopLeft))
	result.WriteString(color.New(color.FgRed, color.Bold).Sprint(strings.Repeat(borderHorizontal, boxWidth)))
	result.WriteString(color.New(color.FgRed, color.Bold).Sprintf(borderTopRight))
	result.WriteString("\n")
	// content lines
	for _, line := range wrappedLines {
		result.WriteString(color.New(color.FgRed, color.Bold).Sprint(borderVertical + " "))
		result.WriteString(color.New(color.FgRed).Sprint(line))
		// add padding to align right border
		paddingSize := boxWidth - len(line) - 2
		if paddingSize > 0 {
			result.WriteString(strings.Repeat(" ", paddingSize))
		}
		result.WriteString(color.New(color.FgRed, color.Bold).Sprint(" " + borderVertical))
		result.WriteString("\n")
	}
	// bottom border
	result.WriteString(color.New(color.FgRed, color.Bold).Sprint(borderBottomLeft))
	result.WriteString(color.New(color.FgRed, color.Bold).Sprint(strings.Repeat(borderHorizontal, boxWidth)))
	result.WriteString(color.New(color.FgRed, color.Bold).Sprint(borderBottomRight))
	return result.String()
}

func (m Model) renderToolField(key, value string) string {
	if value == "" {
		return ""
	}
	line := "  " + key + ": " + value
	maxWidth := m.viewport.Width
	if len(line) <= maxWidth {
		return color.New(color.Faint).Sprint(line)
	}
	const ellipsis = "..."
	if maxWidth <= len(ellipsis) {
		return color.New(color.Faint).Sprint(ellipsis[:maxWidth])
	}
	truncated := line[:maxWidth-len(ellipsis)] + ellipsis
	return color.New(color.Faint).Sprint(truncated)
}

func (m Model) renderToolFields(fields map[string]string) string {
	var parts []string
	fieldOrder := []string{
		"command",
		"path",
		"offset",
		"limit",
		"no line numbers",
		"content",
		"old string",
		"new string",
		"replace all",
		"model",
		"system prompt",
		"user prompt",
		"image files",
		"pdf files",
		"effort",
		"prompt",
		"agents",
	}
	for _, key := range fieldOrder {
		if value, exists := fields[key]; exists && value != "" {
			parts = append(parts, m.renderToolField(key, value))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n" + strings.Join(parts, "\n")
}

func (m Model) renderToolBash(args string) string {
	cmd := gjson.Get(args, "command").String()
	if cmd == "" {
		return ""
	}
	lines := strings.Split(cmd, "\n")
	if len(lines) > 1 {
		cmd = lines[0]
	}
	return m.renderToolFields(map[string]string{"command": cmd})
}

func (m Model) renderToolFSList(args string) string {
	path := gjson.Get(args, "path").String()
	if path == "" {
		return ""
	}
	return m.renderToolFields(map[string]string{"path": path})
}

func (m Model) renderToolFSRead(args string) string {
	path := gjson.Get(args, "path").String()
	if path == "" {
		return ""
	}
	fields := map[string]string{"path": path}
	if offset := gjson.Get(args, "offset").Int(); offset > 0 {
		fields["offset"] = fmt.Sprintf("%d", offset)
	}
	if limit := gjson.Get(args, "limit").Int(); limit > 0 {
		fields["limit"] = fmt.Sprintf("%d", limit)
	}
	if noLineNumbers := gjson.Get(args, "no_line_numbers").Bool(); noLineNumbers {
		fields["no line numbers"] = "true"
	}
	return m.renderToolFields(fields)
}

func (m Model) renderToolFSReplace(args string) string {
	path := gjson.Get(args, "path").String()
	oldString := gjson.Get(args, "old_string").String()
	newString := gjson.Get(args, "new_string").String()
	if path == "" {
		return ""
	}
	fields := map[string]string{"path": path}
	if oldString != "" {
		fields["old string"] = fmt.Sprintf("%d chars", len(oldString))
	}
	if newString != "" {
		fields["new string"] = fmt.Sprintf("%d chars", len(newString))
	}
	if replaceAll := gjson.Get(args, "replace_all").Bool(); replaceAll {
		fields["replace all"] = "true"
	} else {
		fields["replace all"] = "false"
	}
	return m.renderToolFields(fields)
}

func (m Model) renderToolFSWrite(args string) string {
	path := gjson.Get(args, "path").String()
	content := gjson.Get(args, "content").String()
	if path == "" {
		return ""
	}
	fields := map[string]string{"path": path}
	if content != "" {
		fields["content"] = fmt.Sprintf("%d bytes", len(content))
	}
	return m.renderToolFields(fields)
}

func (m Model) renderToolLLM(args string) string {
	model := gjson.Get(args, "model").String()
	userPrompt := gjson.Get(args, "user_prompt").String()
	if model == "" || userPrompt == "" {
		return ""
	}
	fields := map[string]string{"model": model}
	if systemPrompt := gjson.Get(args, "system_prompt").String(); systemPrompt != "" {
		systemPrompt = strings.TrimSpace(systemPrompt)
		wordCount := len(strings.Fields(systemPrompt))
		fields["system prompt"] = fmt.Sprintf("%d words", wordCount)
	}
	userPrompt = strings.TrimSpace(userPrompt)
	wordCount := len(strings.Fields(userPrompt))
	fields["user prompt"] = fmt.Sprintf("%d words", wordCount)
	if imagePaths := gjson.Get(args, "image_paths").Array(); len(imagePaths) > 0 {
		fields["image files"] = fmt.Sprintf("%d", len(imagePaths))
	}
	if pdfPaths := gjson.Get(args, "pdf_paths").Array(); len(pdfPaths) > 0 {
		fields["pdf files"] = fmt.Sprintf("%d", len(pdfPaths))
	}
	return m.renderToolFields(fields)
}

func (m Model) renderToolTask(args string) string {
	effort := gjson.Get(args, "effort").String()
	prompt := gjson.Get(args, "prompt").String()
	agentsData := gjson.Get(args, "agents")
	if effort == "" || prompt == "" || !agentsData.Exists() || !agentsData.IsArray() {
		return ""
	}
	fields := map[string]string{"effort": effort}
	promptLines := strings.Split(prompt, "\n")
	if len(promptLines) > 0 {
		promptLine := strings.TrimSpace(promptLines[0])
		fields["prompt"] = promptLine
	}
	agentCount := len(agentsData.Array())
	fields["agents"] = fmt.Sprintf("%d", agentCount)
	return m.renderToolFields(fields)
}

func (m Model) renderToolThink(args string) string {
	thought := gjson.Get(args, "thought").String()
	if thought == "" {
		return ""
	}
	var s string
	s += "\n"
	s += color.New(color.Faint).Sprint(wrapWithPrefix(thought, "  ", m.viewport.Width))
	return s
}

func (m Model) renderToolTodoRead(_ string) string {
	return ""
}

func (m Model) renderToolTodoWrite(args string) string {
	todosData := gjson.Get(args, "todos")
	if !todosData.Exists() || !todosData.IsArray() {
		return ""
	}
	var todos []string
	for _, todo := range todosData.Array() {
		content := todo.Get("content").String()
		status := todo.Get("status").String()
		if content == "" || status == "" {
			continue
		}
		var checkbox string
		switch status {
		case "completed":
			checkbox = "[x]"
		case "in_progress":
			checkbox = "[~]"
		default:
			checkbox = "[ ]"
		}
		todoLine := fmt.Sprintf("  %s %s", checkbox, content)
		if len(todoLine) > m.viewport.Width {
			todoLine = todoLine[:m.viewport.Width-3] + "..."
		}
		switch status {
		case "completed":
			todoLine = color.New(color.FgGreen).Sprint(todoLine)
		case "in_progress":
			todoLine = color.New(color.FgYellow).Sprint(todoLine)
		}
		todos = append(todos, todoLine)
	}
	if len(todos) == 0 {
		return ""
	}
	return "\n" + strings.Join(todos, "\n")
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
		"anthropic/claude-opus-4",
		"anthropic/claude-sonnet-4",
		"google/gemini-2.5-flash-preview-05-20",
		"google/gemini-2.5-flash-preview-05-20:thinking",
		"google/gemini-2.5-pro-preview",
		"mistralai/devstral-small",
		"openai/codex-mini",
		"openai/gpt-4.1",
		"openai/gpt-4.1-mini",
		"openai/o3",
		"openai/o4-mini-high",
		"qwen/qwen3-32b",
	}
}

func (m Model) getModelSlug(model string) string {
	switch model {
	case "anthropic/claude-opus-4":
		return "claude-opus-4"
	case "anthropic/claude-sonnet-4":
		return "claude-sonnet-4"
	case "google/gemini-2.5-flash-preview-05-20":
		return "gemini-2.5-flash"
	case "google/gemini-2.5-flash-preview-05-20:thinking":
		return "gemini-2.5-flash-thinking"
	case "google/gemini-2.5-pro-preview":
		return "gemini-2.5-pro"
	case "mistralai/devstral-small":
		return "devstral-small"
	case "openai/codex-mini":
		return "codex-mini"
	case "openai/gpt-4.1":
		return "gpt-4.1"
	case "openai/gpt-4.1-mini":
		return "gpt-4.1-mini"
	case "openai/o3":
		return "o3"
	case "openai/o4-mini-high":
		return "o4-mini-high"
	case "qwen/qwen3-32b":
		return "qwen3-32b"
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
		return "copies a message or messages to the clipboard: default, index-based or all."
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
		m.handleCopySlashCommand(fields[1:])
	case "/mode":
		m.handleModeSlashCommand(fields[1:])
	case "/model":
		m.handleModelSlashCommand(fields[1:])
	}
}

func (m *Model) handleClearSlashCommand() {
	m.agent.Reset()
	m.errorMsg = ""
}

func (m *Model) handleCopySlashCommand(args []string) {
	messages, _ := m.agent.GetHistoryState()
	if len(args) > 0 && args[0] == "all" {
		type jsonMessage_ToolCall struct {
			FuncName string          `json:"func_name"`
			FuncArgs json.RawMessage `json:"func_args"`
		}
		type jsonMessage struct {
			Role      string                 `json:"role"`
			Text      string                 `json:"text,omitzero"`
			Result    any                    `json:"result,omitzero"`
			ToolCalls []jsonMessage_ToolCall `json:"tool_calls,omitzero"`
		}
		var jsonMessages []jsonMessage
		for _, msg := range messages {
			switch msg.Role {
			case llm.RoleSystem:
				jsonMessages = append(jsonMessages, jsonMessage{
					Role: "system",
					Text: msg.Content.Text(),
				})
			case llm.RoleAssistant:
				var toolCalls []jsonMessage_ToolCall
				for _, call := range msg.ToolCalls {
					toolCalls = append(toolCalls, jsonMessage_ToolCall{
						FuncName: call.Function.Name,
						FuncArgs: json.RawMessage(call.Function.Args),
					})
				}
				jsonMessages = append(jsonMessages, jsonMessage{
					Role:      "assistant",
					Text:      msg.Content.Text(),
					ToolCalls: toolCalls,
				})
			case llm.RoleTool:
				var result any = msg.Content.Text()
				if json.Valid([]byte(msg.Content.Text())) {
					result = json.RawMessage(msg.Content.Text())
				}
				jsonMessages = append(jsonMessages, jsonMessage{
					Role:   "tool",
					Result: result,
				})
			case llm.RoleUser:
				jsonMessages = append(jsonMessages, jsonMessage{
					Role: "user",
					Text: msg.Content.Text(),
				})
			}
		}
		jsonMessagesData, err := json.MarshalIndent(jsonMessages, "", "  ")
		if err != nil {
			m.logger.Error("failed to marshal messages to JSON: %v", err)
			return
		}
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(string(jsonMessagesData))
		if err := cmd.Run(); err != nil {
			m.logger.Error("failed to copy to clipboard: %v", err)
		}
		return
	}
	var assistantMessages []llm.Message
	for _, msg := range messages {
		if msg.Role == llm.RoleAssistant {
			assistantMessages = append(assistantMessages, msg)
		}
	}
	if len(assistantMessages) == 0 {
		return
	}
	var targetMessage llm.Message
	if len(args) > 0 {
		var index int
		if strings.HasPrefix(args[0], "-") {
			var err error
			index, err = strconv.Atoi(args[0][1:])
			if err != nil || index <= 0 || index > len(assistantMessages) {
				return
			}
			targetMessage = assistantMessages[len(assistantMessages)-index]
		} else {
			var err error
			index, err = strconv.Atoi(args[0])
			if err != nil || index < 1 || index > len(assistantMessages) {
				return
			}
			targetMessage = assistantMessages[index-1]
		}
	} else {
		targetMessage = assistantMessages[len(assistantMessages)-1]
	}
	content := targetMessage.Content.Text()
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
			model := m.createModelInstance(m.openRouterModel)
			m.registerTools(model)
			m.configureAgentModel(m.openRouterModel, model)
			return
		}
	}
}

func (m Model) createModelInstance(modelName string) llm.Model {
	if modelName == "anthropic/claude-sonnet-4" {
		return llm.NewAnthropic(m.logger, m.anthropicKey, "claude-sonnet-4-20250514")
	}
	if modelName == "anthropic/claude-opus-4" {
		return llm.NewAnthropic(m.logger, m.anthropicKey, "claude-opus-4-20240620")
	}
	if modelName == "mistralai/devstral-small" {
		return llm.NewOpenRouter(m.logger, m.openRouterKey, modelName,
			llm.WithOpenRouterOrderProviders([]string{"Mistral"}, false),
			llm.WithOpenRouterRequestTransform(llm.NewOpenRouterHexadecimalToolCallIDRequestTransform()),
		)
	}
	if modelName == "qwen/qwen3-32b" {
		return llm.NewOpenRouter(m.logger, m.openRouterKey, modelName,
			llm.WithOpenRouterOrderProviders([]string{"Cerebras"}, false),
		)
	}
	return llm.NewOpenRouter(m.logger, m.openRouterKey, modelName)
}

func (m Model) configureAgentModel(modelName string, model llm.Model) {
	if modelName == "qwen/qwen3-32b" {
		// the context window is only 32,768 tokens, so the output tokens must be significantly lower
		m.agent.SetModel(model,
			llm.WithMaxTokens(8_192),
			llm.WithReasoningEffortMedium(),
		)
		return
	}
	m.agent.SetModel(model,
		llm.WithMaxTokens(32_768),
		llm.WithReasoningEffortMedium(),
	)
}
