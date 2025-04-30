package main

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
	"github.com/markusylisiurunen/ikm/internal/model"
)

var (
	colorAccent = color.RGB(240, 150, 0)
)

type AgentEventMsg struct {
	name string
}

type Model struct {
	agent      *Agent
	viewport   viewport.Model
	textinput  textinput.Model
	cancelFunc context.CancelFunc
	lastErr    error
}

func initialModel() Model {
	ti := textinput.New()
	ti.Prompt = "\u276F "
	ti.Placeholder = "ask anything"
	ti.Focus()
	ti.CharLimit = 1024
	vp := viewport.New(0, 0)
	vp.KeyMap.Up.SetKeys("up")
	vp.KeyMap.Down.SetKeys("down")
	vp.KeyMap.PageUp.SetEnabled(false)
	vp.KeyMap.PageDown.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	return Model{
		agent:      NewAgent(),
		viewport:   vp,
		textinput:  ti,
		cancelFunc: nil,
	}
}

func listenEventsCmd(m *Model) tea.Cmd {
	return func() tea.Msg {
		event := <-m.agent.events
		switch event := event.(type) {
		case ErrAgentEvent:
			if errors.Is(event.err, context.Canceled) {
				return AgentEventMsg{"canceled"}
			}
			return AgentEventMsg{"error: " + event.err.Error()}
		case TurnCompletedAgentEvent:
			return AgentEventMsg{"done"}
		default:
			return AgentEventMsg{"changed"}
		}
	}
}

func startTurnCmd(m *Model) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	return func() tea.Msg {
		m.agent.StartTurn(ctx)
		return nil
	}
}

func continueTurnCmd(m *Model) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	return func() tea.Msg {
		m.agent.ContinueTurn(ctx)
		return nil
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, listenEventsCmd(&m))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(AgentEventMsg); ok {
		m.viewport.SetContent(m.renderMessages())
		if env.Mode == ModeAgent {
			m.viewport.GotoBottom()
		}
		switch msg.name {
		case "canceled":
			m.cancelFunc = nil
			m.viewport.SetContent(m.renderMessages() + "\n\n" + color.New(color.Faint).Sprint("canceled."))
			m.viewport.GotoBottom()
			return m, listenEventsCmd(&m)
		case "changed":
			return m, listenEventsCmd(&m)
		case "done":
			m.cancelFunc = nil
			if m.agent.active {
				return m, tea.Batch(listenEventsCmd(&m), continueTurnCmd(&m))
			}
			return m, listenEventsCmd(&m)
		default:
			if strings.HasPrefix(msg.name, "error: ") {
				m.cancelFunc = nil
				m.lastErr = fmt.Errorf("%s", msg.name)
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()
				return m, listenEventsCmd(&m)
			}
			panic(fmt.Sprintf("unknown event: %s", msg.name))
		}
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEsc && m.agent.active && m.cancelFunc != nil {
			m.cancelFunc()
			m.cancelFunc = nil
			return m, nil
		}
		if msg.Type == tea.KeyEnter {
			if strings.HasPrefix(m.textinput.Value(), "/") {
				return m.handleSlash()
			}
			return m.handleSend()
		}
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 5
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.textinput.Width = msg.Width - 2
		return m, nil
	}
	var cmd1, cmd2 tea.Cmd
	m.viewport, cmd1 = m.viewport.Update(msg)
	m.textinput, cmd2 = m.textinput.Update(msg)
	return m, tea.Batch(cmd1, cmd2)
}

func (m Model) handleSlash() (tea.Model, tea.Cmd) {
	parts := strings.SplitN(m.textinput.Value(), " ", 2)
	if len(parts) == 0 {
		return m, nil
	}
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	cmd, args := parts[0], parts[1]
	switch cmd {
	case "/clear":
		m.agent.Reset()
		m.viewport.SetContent("")
		m.viewport.GotoTop()
		m.textinput.SetValue("")
		return m, nil
	case "/copy":
		var content string
		for _, i := range slices.Backward(m.agent.history) {
			if i.Role == "assistant" {
				content = i.ContentParts.String()
				break
			}
		}
		if content != "" {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(content)
			if err := cmd.Run(); err != nil {
				debugString("error copying to clipboard: %v", err)
				return m, nil
			}
			m.viewport.SetContent(m.renderMessages() + "\n\n" + color.New(color.Faint).Sprint("copied to clipboard."))
			m.viewport.GotoBottom()
			m.textinput.SetValue("")
		}
		return m, nil
	case "/mode":
		if args != "raw" && args != "dev" && args != "agent" {
			return m, nil
		}
		env.Mode = Mode(args)
		m.textinput.SetValue("")
		return m, nil
	case "/model":
		models := []OpenRouterModel{}
		for _, m := range availableModels {
			if strings.HasPrefix(m.Name, args) {
				models = append(models, m)
			}
		}
		if len(models) == 0 {
			return m, nil
		}
		if len(models) > 1 {
			for _, m := range models {
				if strings.EqualFold(m.Name, args) {
					models = []OpenRouterModel{m}
					break
				}
			}
		}
		if len(models) != 1 {
			return m, nil
		}
		m.agent.client.SetModel(models[0])
		m.textinput.SetValue("")
		return m, nil
	}
	return m, nil
}

func (m Model) handleSend() (tea.Model, tea.Cmd) {
	if m.agent.active || m.textinput.Value() == "" {
		return m, nil
	}
	message := m.textinput.Value()
	m.agent.history = append(m.agent.history, model.Message{Role: "user", Content: model.ContentParts{model.NewTextContentPart(message)}})
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	m.textinput.SetValue("")
	m.lastErr = nil
	return m, startTurnCmd(&m)
}

func (m Model) View() string {
	var s string
	s += m.renderViewport()
	s += "\n\n" + m.textinput.View()
	s += "\n\n" + m.renderFooter()
	return s
}

func (m Model) renderFooter() string {
	if m.agent.active {
		var label string
		label += "working..."
		label += " (" + string(env.Mode)
		label += ", " + m.agent.client.model.Name
		label += ", total: " + m.formatCost(m.agent.totalCost)
		label += ", turn: " + m.formatCost(m.agent.turnCost)
		label += ", tokens: " + fmt.Sprint(m.agent.currTurnTokens)
		label += ", cached: " + fmt.Sprint(m.agent.currTurnCachedTokens) + ")"
		return color.New(color.Faint).Sprint(label)
	}
	if strings.HasPrefix(m.textinput.Value(), "/") {
		for _, cmd := range []string{"/clear", "/copy", "/mode", "/model"} {
			if strings.HasPrefix(m.textinput.Value(), cmd+" ") {
				switch cmd {
				case "/clear":
					return color.New(color.Faint).Sprint("clears the conversation history")
				case "/copy":
					return color.New(color.Faint).Sprint("copies the last assistant message to clipboard")
				case "/mode":
					return color.New(color.Faint).Sprint(`sets the mode to "raw", "agent" or "dev"`)
				case "/model":
					prefix := strings.TrimLeft(strings.TrimPrefix(m.textinput.Value(), cmd), " ")
					matches := []string{}
					for _, m := range availableModels {
						if strings.HasPrefix(m.Name, prefix) {
							matches = append(matches, m.Name)
						}
					}
					if len(matches) == 0 {
						return color.New(color.Faint).Sprint("no models found")
					}
					return color.New(color.Faint).Sprint(strings.Join(matches, ", "))
				}
			}
		}
		return color.New(color.Faint).Sprint("commands: /clear, /copy, /mode <mode>, /model <name>")
	}
	var label string
	label += "ctrl+c to quit."
	label += " (" + string(env.Mode)
	label += ", " + m.agent.client.model.Name
	label += ", total: " + m.formatCost(m.agent.totalCost)
	label += ", turn: " + m.formatCost(m.agent.turnCost)
	label += ", tokens: " + fmt.Sprint(m.agent.currTurnTokens)
	label += ", cached: " + fmt.Sprint(m.agent.currTurnCachedTokens) + ")"
	return color.New(color.Faint).Sprint(label)
}

func (m Model) renderViewport() string {
	return m.viewport.View()
}

func (m Model) renderMessages() string {
	blocks := []string{}
	for idx, i := range m.agent.history {
		switch i.Role {
		case "user":
			blocks = append(blocks, m.renderUserMessage(i))
		case "assistant":
			if i.ContentParts.String() != "" {
				blocks = append(blocks, m.renderAgentMessage(i))
			}
			if len(i.ToolCalls) > 0 {
				for _, tc := range i.ToolCalls {
					var result *model.Message
					for j := idx + 1; j < len(m.agent.history); j++ {
						if msg := m.agent.history[j]; msg.Role == "tool" && *msg.ToolCallID == tc.ID {
							result = &msg
							break
						}
					}
					if result != nil {
						// blocks = append(blocks, m.renderToolMessage(tc, *result))
					}
				}
			}
		}
	}
	if m.lastErr != nil {
		blocks = append(blocks, color.New(color.Faint).Sprint(wrap(m.lastErr.Error(), "", m.viewport.Width)))
	}
	return strings.Join(blocks, "\n\n") + "\n\n\n"
}

func (m Model) renderUserMessage(msg model.Message) string {
	content := wrap("\u203A "+msg.Content.Text(), "", m.viewport.Width)
	return color.New(color.Faint).Sprint(strings.TrimSpace(content))
}

func (m Model) renderAgentMessage(msg model.Message) string {
	var margin uint = 0
	dark := styles.DarkStyleConfig
	dark.Document.Color = nil
	dark.Document.Margin = &margin
	dark.Code.Prefix = ""
	dark.Code.Suffix = ""
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStyles(dark),
		glamour.WithWordWrap(m.viewport.Width-5),
	)
	markdown, _ := renderer.Render(msg.Content.Text())
	var content string
	content += colorAccent.Sprint("\u25CF") + color.New(color.Bold).Sprint(" Agent") + "\n"
	content += strings.TrimSpace(markdown)
	return content
}

// func (m Model) renderToolMessage(call OpenRouter_Message_ToolCall, result OpenRouter_Message) string {
// 	var content string
// 	content += colorAccent.Sprint("\u25CF") + color.New(color.Bold).Sprintf(" %s", call.Function.Name)
// 	switch call.Function.Name {
// 	case "bash":
// 		args, output := m.renderBashTool(call, result)
// 		content += fmt.Sprintf("(%s)\n", args)
// 		content += output
// 	case "patch":
// 		args, output := m.renderPatchTool(call, result)
// 		content += fmt.Sprintf("(%s)\n", args)
// 		content += output
// 	case "write":
// 		args, output := m.renderWriteTool(call, result)
// 		content += fmt.Sprintf("(%s)\n", args)
// 		content += output
// 	}
// 	return content
// }
// func (m Model) renderBashTool(call OpenRouter_Message_ToolCall, result OpenRouter_Message) (string, string) {
// 	var args, output string
// 	args = gjson.Get(call.Function.Arguments, "cmd").String()
// 	// code
// 	output += "  " + color.New(color.Faint).Sprint("\u2514 code:") + fmt.Sprintf(" %d", gjson.Get(result.ContentStr, "code").Int()) + "\n"
// 	// stdout
// 	output += "    " + color.New(color.Faint).Sprint("stdout:") + "\n"
// 	stdout := strings.Split(strings.TrimSpace(gjson.Get(result.ContentStr, "stdout").String()), "\n")
// 	stdoutLen := len(stdout)
// 	stdout = stdout[:min(len(stdout), 3)]
// 	if stdoutLen > 0 {
// 		output += wrap(strings.Join(stdout, "\n"), "      ", m.viewport.Width)
// 		if stdoutLen > 3 {
// 			output += "\n      " + color.New(color.Faint).Sprintf("(%d more lines)", stdoutLen-3)
// 		}
// 		output += "\n"
// 	}
// 	// stderr
// 	output += "    " + color.New(color.Faint).Sprint("stderr:") + "\n"
// 	stderr := strings.Split(strings.TrimSpace(gjson.Get(result.ContentStr, "stderr").String()), "\n")
// 	stderrLen := len(stderr)
// 	stderr = stderr[:min(len(stderr), 3)]
// 	if stderrLen > 0 {
// 		output += wrap(strings.Join(stderr, "\n"), "      ", m.viewport.Width)
// 		if stderrLen > 3 {
// 			output += "\n      " + color.New(color.Faint).Sprintf("(%d more lines)", stderrLen-3)
// 		}
// 		output += "\n"
// 	}
// 	return args, strings.TrimRight(output, " \n")
// }
// func (m Model) renderWriteTool(call OpenRouter_Message_ToolCall, _ OpenRouter_Message) (string, string) {
// 	var args, output string
// 	args += color.New(color.Faint).Sprint("file: ") + gjson.Get(call.Function.Arguments, "file").String()
// 	output = "  " + color.New(color.Faint).Sprint("\u2514 done")
// 	return args, output
// }
// func (m Model) renderPatchTool(call OpenRouter_Message_ToolCall, result OpenRouter_Message) (string, string) {
// 	var args, output string
// 	args += color.New(color.Faint).Sprint("file: ") + gjson.Get(call.Function.Arguments, "file").String()
// 	args += color.New(color.Faint).Sprint(" range: ")
// 	args += fmt.Sprintf("%d", gjson.Get(call.Function.Arguments, "range_start").Int()) + "-"
// 	args += fmt.Sprintf("%d", gjson.Get(call.Function.Arguments, "range_end").Int())
// 	editedLines := strings.Split(strings.TrimSpace(gjson.Get(result.ContentStr, "edited_lines").String()), "\n")
// 	editedLinesLen := len(editedLines)
// 	editedLines = editedLines[:min(len(editedLines), 16)]
// 	if editedLinesLen > 0 {
// 		output += "  " + color.New(color.Faint).Sprint("\u2514 edited lines:") + "\n"
// 		output += wrap(strings.Join(editedLines, "\n"), "      ", m.viewport.Width)
// 		if editedLinesLen > 16 {
// 			output += "\n      " + color.New(color.Faint).Sprintf("(%d more lines)", editedLinesLen-16)
// 		}
// 		output += "\n"
// 	}
// 	return args, strings.TrimRight(output, " \n")
// }

func (m Model) formatCost(cost float64) string {
	if cost == 0 {
		return "$0"
	}
	if cost < 1 {
		return fmt.Sprintf("%.1f¢", cost*100)
	}
	return fmt.Sprintf("$%.3f", cost)
}
