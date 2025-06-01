package main

import (
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

func filterPromptForDisabledTools(prompt string, disabledTools []string) string {
	toolToHelpTag := map[string]string{
		"bash":  "bash_tool_help",
		"fs":    "fs_tool_help",
		"llm":   "llm_tool_help",
		"task":  "task_tool_help",
		"think": "think_tool_help",
	}
	for _, tool := range disabledTools {
		if helpTag, exists := toolToHelpTag[tool]; exists {
			pattern := `<` + helpTag + `[^>]*>.*?</` + helpTag + `>`
			re := regexp.MustCompile(`(?s)` + pattern)
			prompt = re.ReplaceAllString(prompt, "")
		}
	}
	return prompt
}

type config struct {
	debug         bool
	disabledTools []string
	mode          string
	model         string
	openRouterKey string
}

func (c *config) read() {
	var (
		debug       = flag.Bool("debug", false, "enable debug logging")
		mode        = flag.String("mode", "raw", "mode to use (agent, dev, raw)")
		model       = flag.String("model", "claude-sonnet-4", "model to use")
		noToolBash  = flag.Bool("no-tool-bash", false, "disable the bash tool")
		noToolFS    = flag.Bool("no-tool-fs", false, "disable the fs tool")
		noToolLLM   = flag.Bool("no-tool-llm", false, "disable the llm tool")
		noToolTask  = flag.Bool("no-tool-task", false, "disable the task tool")
		noToolThink = flag.Bool("no-tool-think", false, "disable the think tool")
	)
	flag.Parse()
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
	c.debug = *debug
	c.mode = *mode
	c.model = *model
	c.openRouterKey = os.Getenv("OPENROUTER_KEY")
}

func main() {
	var cfg config
	cfg.read()
	// validate OpenRouter key
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
	model := tui.Initial(debugLogger, cfg.openRouterKey, runInBashDocker,
		tui.WithStaticMode("agent", filterPromptForDisabledTools(agentPrompt, cfg.disabledTools)),
		tui.WithStaticMode("dev", filterPromptForDisabledTools(devPrompt, cfg.disabledTools)),
		tui.WithStaticMode("raw", filterPromptForDisabledTools(rawPrompt, cfg.disabledTools)),
		tui.WithSetDefaultMode(cfg.mode),
		tui.WithSetDefaultModel(cfg.model),
		tui.WithDisabledTools(cfg.disabledTools),
	)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		log.Fatalf("error running program: %v", err)
	}
}
