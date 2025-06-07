package main

import (
	"bytes"
	_ "embed"
	"flag"
	"log"
	"os"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/internal/tui"
)

//go:embed prompts/agent.txt
var agentPrompt string

//go:embed prompts/dev.txt
var devPrompt string

//go:embed prompts/raw.txt
var rawPrompt string

func injectVariablesToPrompt(prompt string, variables map[string]string) string {
	for key, value := range variables {
		prompt = regexp.MustCompile(`{{\s?`+key+`\s?}}`).ReplaceAllString(prompt, value)
	}
	return prompt
}

func readSystemPromptWithCustomInstructions(systemPromptTemplate string) string {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get current working directory: %v", err)
	}
	customInstructions, err := os.ReadFile(".ikm/instructions.md")
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to read instructions file at %s: %v", ".ikm/instructions.md", err)
	}
	customInstructionsContent := string(bytes.TrimSpace(customInstructions))
	if customInstructionsContent == "" {
		customInstructionsContent = "No custom instructions provided."
	}
	vars := map[string]string{
		"cwd":          cwd,
		"instructions": customInstructionsContent,
	}
	return injectVariablesToPrompt(systemPromptTemplate, vars)
}

type config struct {
	debug           bool
	disabledTools   []string
	reasoningEffort uint8
	mode            string
	model           string
	anthropicKey    string
	openRouterKey   string
}

func (c *config) read() {
	var (
		debug       = flag.Bool("debug", false, "enable debug logging")
		mode        = flag.String("mode", "raw", "mode to use (agent, dev, raw)")
		model       = flag.String("model", "claude-sonnet-4", "model to use")
		reasoning   = flag.String("reasoning", "2", "reasoning effort level (0, 1, 2, 3)")
		noTools     = flag.Bool("no-tools", false, "disable all tools")
		noToolBash  = flag.Bool("no-tool-bash", false, "disable the bash tool")
		noToolFS    = flag.Bool("no-tool-fs", false, "disable the fs tool")
		noToolLLM   = flag.Bool("no-tool-llm", false, "disable the llm tool")
		noToolTask  = flag.Bool("no-tool-task", false, "disable the task tool")
		noToolThink = flag.Bool("no-tool-think", false, "disable the think tool")
		noToolTodo  = flag.Bool("no-tool-todo", false, "disable the todo tool")
	)
	flag.Parse()
	switch *reasoning {
	case "0":
		c.reasoningEffort = 0
	case "1":
		c.reasoningEffort = 1
	case "2":
		c.reasoningEffort = 2
	case "3":
		c.reasoningEffort = 3
	default:
		log.Fatalf("invalid reasoning effort level: %s, must be one of: 0, 1, 2, 3", *reasoning)
	}
	if *noTools {
		*noToolBash = true
		*noToolFS = true
		*noToolLLM = true
		*noToolTask = true
		*noToolThink = true
		*noToolTodo = true
	}
	if *noToolBash {
		c.disabledTools = append(c.disabledTools, "bash")
	}
	if *noToolFS {
		c.disabledTools = append(c.disabledTools, "fs")
	}
	if *noToolLLM {
		c.disabledTools = append(c.disabledTools, "llm")
	}
	if *noToolTask {
		c.disabledTools = append(c.disabledTools, "task")
	}
	if *noToolThink {
		c.disabledTools = append(c.disabledTools, "think")
	}
	if *noToolTodo {
		c.disabledTools = append(c.disabledTools, "todo")
	}
	c.debug = *debug
	c.mode = *mode
	c.model = *model
	c.anthropicKey = os.Getenv("ANTHROPIC_KEY")
	c.openRouterKey = os.Getenv("OPENROUTER_KEY")
}

func main() {
	var cfg config
	cfg.read()
	// validate API keys
	if cfg.anthropicKey == "" {
		log.Fatal("ANTHROPIC_KEY environment variable is not set")
	}
	if cfg.openRouterKey == "" {
		log.Fatal("OPENROUTER_KEY environment variable is not set")
	}
	// setup the Docker container for running bash commands
	if err := buildBashDockerIfNeeded(); err != nil {
		log.Fatalf("error building bash docker image: %v", err)
	}
	// if in debug mode, create a debug log file
	var debugLogger logger.Logger = logger.NoOp()
	if cfg.debug {
		if err := os.MkdirAll(".ikm/logs", 0755); err != nil {
			log.Fatalf("error creating debug folder: %v", err)
		}
		debugLogFile := time.Now().Format("2006-01-02T15:04:05") + ".log"
		f, err := os.OpenFile(".ikm/logs/"+debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		defer f.Close() //nolint:errcheck
		debugLogger = logger.New(f)
		debugLogger.SetEnabled(true)
		debugLogger.SetLevel("debug")
	}
	// init the terminal UI model and run the program
	if cfg.mode != "agent" && cfg.mode != "dev" && cfg.mode != "raw" {
		log.Fatalf("invalid mode: %s, must be one of: agent, dev, raw", cfg.mode)
	}
	model := tui.Initial(debugLogger, cfg.anthropicKey, cfg.openRouterKey, runInBashDocker,
		tui.WithDynamicMode("agent", func() string { return readSystemPromptWithCustomInstructions(agentPrompt) }),
		tui.WithDynamicMode("dev", func() string { return readSystemPromptWithCustomInstructions(devPrompt) }),
		tui.WithDynamicMode("raw", func() string { return readSystemPromptWithCustomInstructions(rawPrompt) }),
		tui.WithSetDefaultMode(cfg.mode),
		tui.WithSetDefaultModel(cfg.model),
		tui.WithDisabledTools(cfg.disabledTools),
		tui.WithReasoningEffort(cfg.reasoningEffort),
	)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		log.Fatalf("error running program: %v", err)
	}
}
