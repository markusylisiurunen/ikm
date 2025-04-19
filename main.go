package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/fatih/color"
	"github.com/tidwall/gjson"
)

// debugging ---------------------------------------------------------------------------------------

var (
	debugMode   = os.Getenv("DEBUG") == "1"
	debugFolder = ".logs"
	debugSuffix = time.Now().Format("2006-01-02-15:04:05")
)

func debug(data json.RawMessage) {
	if debugMode {
		if err := os.MkdirAll(debugFolder, 0755); err != nil {
			panic(fmt.Sprintf("error creating debug folder: %v", err))
		}
		debugLogFile := "juttele-debug-" + debugSuffix + ".log"
		f, err := os.OpenFile(debugFolder+"/"+debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(fmt.Sprintf("error opening log file: %v", err))
		}
		defer f.Close()
		type LogLine struct {
			Ts   string          `json:"ts"`
			Data json.RawMessage `json:"data"`
		}
		line, err := json.Marshal(LogLine{
			Ts:   time.Now().Format(time.RFC3339),
			Data: data,
		})
		if err != nil {
			panic(fmt.Sprintf("error marshalling log line: %v", err))
		}
		line = append(line, []byte("\n")...)
		if _, err := f.Write(line); err != nil {
			panic(fmt.Sprintf("error writing to log file: %v", err))
		}
	}
}
func debugString(msg string, args ...any) {
	d, err := json.Marshal(map[string]any{"msg": fmt.Sprintf(msg, args...)})
	if err != nil {
		panic(fmt.Sprintf("error marshalling debug message: %v", err))
	}
	debug(d)
}
func debugAny(data any) {
	d, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("error marshalling debug data: %v", err))
	}
	debug(d)
}

// openrouter client -------------------------------------------------------------------------------

//go:embed prompts/agent.txt
var systemPrompt string

type Model string

func (m Model) Name() string {
	parts := strings.SplitN(string(m), "/", 2)
	if len(parts) != 2 || len(parts[1]) == 0 {
		return string(m)
	}
	return strings.ReplaceAll(parts[1], ":", "-")
}

const (
	Model_Claude_3_7_Sonnet                 Model = "anthropic/claude-3.7-sonnet"
	Model_Claude_3_7_Sonnet_Thinking        Model = "anthropic/claude-3.7-sonnet:thinking"
	Model_GPT_4_1                           Model = "openai/gpt-4.1"
	Model_Gemini_2_5_Flash_Preview          Model = "google/gemini-2.5-flash-preview"
	Model_Gemini_2_5_Flash_Preview_Thinking Model = "google/gemini-2.5-flash-preview:thinking"
	Model_Gemini_2_5_Pro_Preview            Model = "google/gemini-2.5-pro-preview-03-25"
	Model_o3                                Model = "openai/o3"
	Model_o4_Mini_High                      Model = "openai/o4-mini-high"
)

var (
	defaultModel = Model_Gemini_2_5_Flash_Preview
	allModels    = []Model{
		Model_Claude_3_7_Sonnet,
		Model_Claude_3_7_Sonnet_Thinking,
		Model_GPT_4_1,
		Model_Gemini_2_5_Flash_Preview,
		Model_Gemini_2_5_Flash_Preview_Thinking,
		Model_Gemini_2_5_Pro_Preview,
		Model_o3,
		Model_o4_Mini_High,
	}
)

type OpenRouter_Message_ToolCall_Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type OpenRouter_Message_ToolCall struct {
	ID       string                                `json:"id"`
	Type     string                                `json:"type"`
	Function *OpenRouter_Message_ToolCall_Function `json:"function,omitempty"`
}
type OpenRouter_Message struct {
	Content    string                        `json:"content"`
	Role       string                        `json:"role"`
	ToolCallID *string                       `json:"tool_call_id,omitempty"`
	ToolCalls  []OpenRouter_Message_ToolCall `json:"tool_calls,omitempty"`
	ToolName   *string                       `json:"name,omitempty"`
}

type OpenRouterRequest_Tool_Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type OpenRouterRequest_Tool struct {
	Type     string                           `json:"type"`
	Function *OpenRouterRequest_Tool_Function `json:"function,omitempty"`
}
type OpenRouterRequest_Usage struct {
	Include bool `json:"include"`
}
type OpenRouterRequest struct {
	MaxTokens   int                      `json:"max_tokens"`
	Messages    []OpenRouter_Message     `json:"messages"`
	Model       string                   `json:"model"`
	Temperature float64                  `json:"temperature"`
	Tools       []OpenRouterRequest_Tool `json:"tools,omitempty"`
	Usage       OpenRouterRequest_Usage  `json:"usage"`
}

type OpenRouterResponse_Error struct {
	Code     int            `json:"code"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata"`
}
type OpenRouterResponse_Choice struct {
	Error              *OpenRouterResponse_Error `json:"error"`
	FinishReason       *string                   `json:"finish_reason"`
	Message            *OpenRouter_Message       `json:"message"`
	NativeFinishReason *string                   `json:"native_finish_reason"`
}
type OpenRouterResponse_Usage struct {
	CompletionTokens int     `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
	PromptTokens     int     `json:"prompt_tokens"`
	TotalTokens      int     `json:"total_tokens"`
}
type OpenRouterResponse struct {
	Choices []OpenRouterResponse_Choice `json:"choices"`
	Created int                         `json:"created"`
	Error   *OpenRouterResponse_Error   `json:"error"`
	ID      string                      `json:"id"`
	Model   string                      `json:"model"`
	Object  string                      `json:"object"`
	Usage   OpenRouterResponse_Usage    `json:"usage"`
}

type OpenRouter struct {
	apiKey      string
	maxTokens   int
	model       Model
	temperature float64
	tools       []Tool
}

const (
	defaultOpenRouterMaxTokens   = 16_384
	defaultOpenRouterTemperature = 0.2
)

func NewOpenRouter(apiKey string, model Model, tools []Tool) *OpenRouter {
	return &OpenRouter{
		apiKey:      apiKey,
		maxTokens:   defaultOpenRouterMaxTokens,
		model:       model,
		temperature: defaultOpenRouterTemperature,
		tools:       tools,
	}
}

func (c *OpenRouter) Reset() {
	c.maxTokens = defaultOpenRouterMaxTokens
	c.temperature = defaultOpenRouterTemperature
}

func (c *OpenRouter) SetMaxTokens(maxTokens int)  { c.maxTokens = max(512, maxTokens) }
func (c *OpenRouter) SetModel(model Model)        { c.model = model }
func (c *OpenRouter) SetTemperature(temp float64) { c.temperature = max(0, min(2, temp)) }

func (c *OpenRouter) Call(ctx context.Context, messages []OpenRouter_Message) (*OpenRouterResponse, error) {
	payload := OpenRouterRequest{
		MaxTokens:   c.maxTokens,
		Messages:    messages,
		Model:       string(c.model),
		Temperature: c.temperature,
		Usage:       OpenRouterRequest_Usage{Include: true},
	}
	if len(c.tools) > 0 {
		payload.Tools = make([]OpenRouterRequest_Tool, 0, len(c.tools))
		for _, tool := range c.tools {
			payload.Tools = append(payload.Tools, tool.Definition())
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}
	debugAny(map[string]any{"msg": "openrouter request", "req": json.RawMessage(data)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions",
		bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 180 * time.Second /* 3 min */}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}
	debugAny(map[string]any{"msg": "openrouter response", "resp": json.RawMessage(body)})
	var result OpenRouterResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %w; response: %s", err, string(body))
	}
	if result.Error != nil {
		return nil, fmt.Errorf("error from model: %s; code: %d; metadata: %v",
			result.Error.Message, result.Error.Code, result.Error.Metadata)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from model")
	}
	if choice := result.Choices[0]; choice.Error != nil {
		return nil, fmt.Errorf("error from model: %s; code: %d; metadata: %v",
			choice.Error.Message, choice.Error.Code, choice.Error.Metadata)
	}
	return &result, nil
}

// tools -------------------------------------------------------------------------------------------

type Tool interface {
	Definition() OpenRouterRequest_Tool
	Execute(ctx context.Context, args string) (string, error)
}

// bash tool ---------------------------------------------------------------------------------------

type bashTool struct{}

func (b bashTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "bash",
			Description: strings.Join([]string{
				"Execute a bash command in the current working directory in an Ubuntu sandbox.",
				"You are allowed to run read-only commands like 'ls', 'cat', 'sed', 'wc', 'head', 'tail', and so on.",
				"Network is disabled in the sandbox.",
				"You do not need to ask for permission to run these commands.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"cmd": {
						"type": "string",
						"description": "The bash command to execute."
					}
				},
				"required": ["cmd"]
			}`),
		},
	}
}

func (b bashTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", ".:/sandbox:ro",
		"-w", "/sandbox",
		"--network", "none",
		"ubuntu:noble",
		"bash", "-c", params.Cmd)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error executing command: %w", err)
	}
	code := cmd.ProcessState.ExitCode()
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	out, err := json.Marshal(struct {
		Code   int    `json:"code"`
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}{
		Code:   code,
		Stdout: strings.TrimSpace(stdout),
		Stderr: strings.TrimSpace(stderr),
	})
	if err != nil {
		return "", fmt.Errorf("error marshalling output: %w", err)
	}
	return string(out), nil
}

// write tool --------------------------------------------------------------------------------------

type writeTool struct{}

func (w writeTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "write",
			Description: strings.Join([]string{
				"Write a file in the current working directory.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file": {
						"type": "string",
						"description": "The file to write to (e.g., 'scripts/say.py')."
					},
					"content": {
						"type": "string",
						"description": "The content to write to the file."
					}
				},
				"required": ["file", "content"]
			}`),
		},
	}
}

func (w writeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	if params.File == "" || params.Content == "" {
		return "", fmt.Errorf("file name or content is empty")
	}
	if ok, err := w.isPathWithinRoot(params.File); err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	} else if !ok {
		return "", fmt.Errorf("path %s is not within the root directory", params.File)
	}
	cleanPath := filepath.Clean(params.File)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return "", fmt.Errorf("error creating directory: %w", err)
	}
	if err := os.WriteFile(cleanPath, []byte(params.Content), 0644); err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}
	return "", nil
}

func (w writeTool) isPathWithinRoot(targetPath string) (bool, error) {
	rootDir, err := filepath.Abs(".")
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}
	if !strings.HasSuffix(rootDir, string(filepath.Separator)) {
		rootDir += string(filepath.Separator)
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of target: %w", err)
	}
	if !strings.HasPrefix(targetAbs, rootDir) {
		return false, nil
	}
	cleanPath := filepath.Clean(targetPath)
	if targetPath != cleanPath && strings.Contains(targetPath, "..") {
		return false, errors.New("path contains suspicious directory traversal elements")
	}
	return true, nil
}

// patch tool --------------------------------------------------------------------------------------

type patchTool struct{}

func (p patchTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "patch",
			Description: strings.Join([]string{
				"Edit a file by replacing a row range with a patch.",
				"It is highly advised to include a few context lines before and after the actual edit to avoid confusion.",
				"Understand that the original content at the line range will be *replaced* with the new content, not modified.",
				"Every time you are about to use this tool, you MUST first read the file range with line numbers you are about to patch even if you have read it before. The file might have changed.",
				"If you know you are about to make an edit around line 10, you can just read this range with a few context lines before and after, like 5-15. For example, 'cat -n file | sed -n 5,15p'.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file": {
						"type": "string",
						"description": "The file to apply the patch to."
					},
					"range_start": {
						"type": "number",
						"description": "The start of the line range to replace (index starts at 1)."
					},
					"range_end": {
						"type": "number",
						"description": "The end of the line range to replace."
					},
					"content": {
						"type": "string",
						"description": "The new content to replace the line range with."
					}
				},
				"required": ["file", "range_start", "range_end", "content"]
			}`),
		},
	}
}

func (p patchTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		File    string `json:"file"`
		Start   int    `json:"range_start"`
		End     int    `json:"range_end"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	content, err := os.ReadFile(params.File)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	if params.Start < 1 || params.End > len(lines) {
		return "", fmt.Errorf("invalid line range: %d-%d (file has %d lines)", params.Start, params.End, len(lines))
	}
	if params.Start > params.End {
		return "", fmt.Errorf("start line %d is greater than end line %d", params.Start, params.End)
	}
	edited := make([]string, 0, len(lines)+len(strings.Split(params.Content, "\n")))
	edited = append(edited, lines[:params.Start-1]...)
	edited = append(edited, strings.Split(params.Content, "\n")...)
	edited = append(edited, lines[params.End:]...)
	if err := os.WriteFile(params.File, []byte(strings.Join(edited, "\n")), 0644); err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}
	return "", nil
}

// agent -------------------------------------------------------------------------------------------

type Agent struct {
	client  *OpenRouter
	active  bool
	history []OpenRouter_Message
	tools   []Tool
	cost    float64
}

func NewAgent() *Agent {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		panic("OPENROUTER_API_KEY environment variable not set")
	}
	tools := []Tool{bashTool{}, writeTool{}, patchTool{}}
	agent := &Agent{
		client:  NewOpenRouter(apiKey, defaultModel, tools),
		active:  false,
		history: []OpenRouter_Message{},
		tools:   tools,
		cost:    0,
	}
	agent.appendSystemInstructions()
	return agent
}

func (a *Agent) Reset() {
	a.client.Reset()
	a.active = false
	a.cost = 0
	a.history = []OpenRouter_Message{}
	a.appendSystemInstructions()
}

func (a *Agent) StartTurn(message string) error {
	a.active = true
	a.history = append(a.history, OpenRouter_Message{Role: "user", Content: message})
	return a.ContinueTurn()
}

func (a *Agent) ContinueTurn() error {
	if !a.active {
		return fmt.Errorf("agent is not active")
	}
	answer, err := a.client.Call(context.Background(), a.history)
	if err != nil {
		a.active = false
		return err
	}
	a.cost += answer.Usage.Cost
	choice := answer.Choices[0]
	a.active = len(choice.Message.ToolCalls) > 0
	a.history = append(a.history, *choice.Message)
	if err := a.executeToolCalls(); err != nil {
		a.active = false
		return err
	}
	return nil
}

func (a *Agent) executeToolCalls() error {
	if !a.active || len(a.history) == 0 {
		return nil
	}
	last := a.history[len(a.history)-1]
	if len(last.ToolCalls) == 0 {
		return nil
	}
	for _, tc := range last.ToolCalls {
		if tc.Type != "function" {
			return fmt.Errorf("tool call type %s not supported", tc.Type)
		}
		debugAny(map[string]any{"msg": "processing a tool call", "tool_call": tc})
		tool := a.getTool(tc.Function.Name)
		if tool == nil {
			return fmt.Errorf("tool %s not found", tc.Function.Name)
		}
		result, err := tool.Execute(context.Background(), tc.Function.Arguments)
		msg := OpenRouter_Message{
			Role:       "tool",
			ToolCallID: &tc.ID,
			ToolName:   &tc.Function.Name,
		}
		if err != nil {
			debugString("error executing tool %s: %v", tc.Function.Name, err)
			msg.Content = fmt.Sprintf("Error: %v", err)
		} else {
			debugString("tool %s executed successfully: %q", tc.Function.Name, result)
			msg.Content = result
		}
		a.history = append(a.history, msg)
	}
	return nil
}

func (a *Agent) appendSystemInstructions() {
	// TODO: inject custom instructions to the system prompt
	content := strings.TrimSpace(systemPrompt)
	a.history = append(a.history, OpenRouter_Message{Role: "system", Content: content})
}

func (a *Agent) getTool(name string) Tool {
	for _, t := range a.tools {
		if t.Definition().Function.Name == name {
			return t
		}
	}
	return nil
}

// terminal ui -------------------------------------------------------------------------------------

var (
	colorAccent = color.RGB(240, 150, 0)
)

type agentTurnCompleteMsg struct {
	err error
}

func startTurnCmd(agent *Agent, message string) tea.Cmd {
	return func() tea.Msg { return agentTurnCompleteMsg{agent.StartTurn(message)} }
}
func continueTurnCmd(agent *Agent) tea.Cmd {
	return func() tea.Msg { return agentTurnCompleteMsg{agent.ContinueTurn()} }
}

type model struct {
	agent     *Agent
	viewport  viewport.Model
	textinput textinput.Model
}

func initialModel() model {
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
	return model{
		agent:     NewAgent(),
		viewport:  vp,
		textinput: ti,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(agentTurnCompleteMsg); ok {
		return m.handleAgentTurnComplete(msg)
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEsc && m.agent.active {
			// TODO: stop the agent
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
		m.viewport.GotoBottom()
		m.textinput.Width = msg.Width
		return m, nil
	}
	var cmd1, cmd2 tea.Cmd
	m.viewport, cmd1 = m.viewport.Update(msg)
	m.textinput, cmd2 = m.textinput.Update(msg)
	return m, tea.Batch(cmd1, cmd2)
}
func (m model) handleAgentTurnComplete(msg agentTurnCompleteMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// TODO: show the error to the user
		debugString("error: %v", msg.err)
		return m, nil
	}
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	if m.agent.active {
		return m, continueTurnCmd(m.agent)
	}
	return m, nil
}
func (m model) handleSlash() (tea.Model, tea.Cmd) {
	parts := strings.SplitN(m.textinput.Value(), " ", 2)
	if len(parts) < 2 {
		return m, nil
	}
	cmd, args := parts[0], parts[1]
	switch cmd {
	case "/clear":
		m.agent.Reset()
		m.viewport.SetContent("")
		m.viewport.GotoTop()
		m.textinput.SetValue("")
		return m, nil
	case "/model":
		var model Model
		for _, m := range allModels {
			if strings.EqualFold(m.Name(), args) {
				model = m
				break
			}
		}
		if model != "" {
			m.agent.client.SetModel(model)
		}
		m.textinput.SetValue("")
		return m, nil
	}
	return m, nil
}
func (m model) handleSend() (tea.Model, tea.Cmd) {
	if m.agent.active || m.textinput.Value() == "" {
		return m, nil
	}
	message := m.textinput.Value()
	m.textinput.SetValue("")
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	return m, startTurnCmd(m.agent, message)
}

func (m model) View() string {
	var s string
	s += m.renderViewport()
	s += "\n\n" + m.textinput.View()
	if strings.HasPrefix(m.textinput.Value(), "/") {
		cmds := []string{"/clear", "/models", "/model <model_name>"}
		s += "\n\n" + color.New(color.Faint, color.FgWhite).Sprint("available commands: "+strings.Join(cmds, ", "))
	} else {
		if m.agent.active {
			s += "\n\n" + color.New(color.Faint, color.FgWhite).Sprint("thinking...")
		} else {
			var ss string
			ss += "press ctrl+c to quit."
			ss += fmt.Sprintf(" (model: %s, cost: %.5f)", m.agent.client.model.Name(), m.agent.cost)
			s += "\n\n" + color.New(color.Faint, color.FgWhite).Sprint(ss)
		}
	}
	return s
}

func (m model) renderViewport() string {
	return m.viewport.View()
}

func (m model) renderMessages() string {
	blocks := []string{}
	for _, i := range m.agent.history {
		switch i.Role {
		case "user":
			blocks = append(blocks, m.renderUserMessage(i))
		case "assistant":
			if i.Content != "" {
				blocks = append(blocks, m.renderAgentMessage(i))
			}
			if len(i.ToolCalls) > 0 {
				for _, tc := range i.ToolCalls {
					var result *OpenRouter_Message
					for _, msg := range slices.Backward(m.agent.history) {
						if msg.Role == "tool" && *msg.ToolCallID == tc.ID {
							result = &msg
							break
						}
					}
					if result != nil {
						blocks = append(blocks, m.renderToolMessage(tc, *result))
					}
				}
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (m model) renderUserMessage(msg OpenRouter_Message) string {
	content := wrap("\u203A "+msg.Content, "", m.viewport.Width)
	return color.New(color.Faint).Sprint(strings.TrimSpace(content))
}

func (m model) renderAgentMessage(msg OpenRouter_Message) string {
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
	markdown, _ := renderer.Render(msg.Content)
	var content string
	content += colorAccent.Sprint("\u25CF") + color.New(color.Bold).Sprint(" Agent") + "\n"
	content += strings.TrimSpace(markdown)
	return content
}

func (m model) renderToolMessage(call OpenRouter_Message_ToolCall, result OpenRouter_Message) string {
	var content string
	content += colorAccent.Sprint("\u25CF") + color.New(color.Bold).Sprintf(" %s", call.Function.Name)
	switch call.Function.Name {
	case "bash":
		args, output := m.renderBashTool(call, result)
		content += fmt.Sprintf("(%s)\n", args)
		content += output
	case "write":
		args, output := m.renderWriteTool(call, result)
		content += fmt.Sprintf("(%s)\n", args)
		content += output
	case "patch":
		args, output := m.renderPatchTool(call, result)
		content += fmt.Sprintf("(%s)\n", args)
		content += output
	}
	return content
}
func (m model) renderBashTool(call OpenRouter_Message_ToolCall, result OpenRouter_Message) (string, string) {
	var args, output string
	args = gjson.Get(call.Function.Arguments, "cmd").String()
	// code
	output += "  " + color.New(color.Faint).Sprint("\u2514 code:") + fmt.Sprintf(" %d", gjson.Get(result.Content, "code").Int()) + "\n"
	// stdout
	output += "    " + color.New(color.Faint).Sprint("stdout:") + "\n"
	stdout := strings.Split(gjson.Get(result.Content, "stdout").String(), "\n")
	stdoutLen := len(stdout)
	stdout = stdout[:min(len(stdout), 3)]
	output += wrap(strings.Join(stdout, "\n"), "      ", m.viewport.Width)
	if stdoutLen > 3 {
		output += "\n      " + color.New(color.Faint).Sprintf("(%d more lines)", stdoutLen-3) + "\n"
	}
	// stderr
	output += "    " + color.New(color.Faint).Sprint("stderr:") + "\n"
	stderr := strings.Split(gjson.Get(result.Content, "stderr").String(), "\n")
	stderrLen := len(stderr)
	stderr = stderr[:min(len(stderr), 3)]
	output += wrap(strings.Join(stderr, "\n"), "      ", m.viewport.Width)
	if stderrLen > 3 {
		output += "\n      " + color.New(color.Faint).Sprintf("(%d more lines)", stderrLen-3) + "\n"
	}
	return args, strings.TrimRight(output, " \n")
}
func (m model) renderWriteTool(call OpenRouter_Message_ToolCall, _ OpenRouter_Message) (string, string) {
	var args, output string
	args += color.New(color.Faint).Sprint("file: ") + gjson.Get(call.Function.Arguments, "file").String()
	output = "  " + color.New(color.Faint).Sprint("\u2514 done")
	return args, output
}
func (m model) renderPatchTool(call OpenRouter_Message_ToolCall, _ OpenRouter_Message) (string, string) {
	var args, output string
	args += color.New(color.Faint).Sprint("file: ") + gjson.Get(call.Function.Arguments, "file").String()
	args += color.New(color.Faint).Sprint(" range: ")
	args += fmt.Sprintf("%d", gjson.Get(call.Function.Arguments, "range_start").Int()) + "-"
	args += fmt.Sprintf("%d", gjson.Get(call.Function.Arguments, "range_end").Int())
	output = "  " + color.New(color.Faint).Sprint("\u2514 done")
	return args, output
}

// utility functions -------------------------------------------------------------------------------

func wrap(s string, prefix string, width int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		wrapped := wrapLine(line, width-utf8.RuneCountInString(prefix))
		for j, wline := range wrapped {
			wrapped[j] = prefix + wline
		}
		lines[i] = strings.Join(wrapped, "\n")
	}
	return strings.Join(lines, "\n")
}
func wrapLine(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var b strings.Builder
	curlen := 0
	words := strings.Split(s, " ")
	for i, word := range words {
		wlen := utf8.RuneCountInString(word)
		if i == 0 {
			b.WriteString(word)
			curlen += wlen
		} else if curlen+1+wlen <= width {
			b.WriteByte(' ')
			b.WriteString(word)
			curlen += 1 + wlen
		} else {
			b.WriteString("\n" + word)
			curlen = wlen
		}
	}
	return strings.Split(b.String(), "\n")
}

// main --------------------------------------------------------------------------------------------

func main() {
	m := initialModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}
