package main

import (
	_ "embed"
	"log"
	"os"
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

type config struct {
	debug         bool
	mode          string
	model         string
	openRouterKey string
}

func (c *config) read() {
	c.debug = os.Getenv("DEBUG") == "1" || os.Getenv("DEBUG") == "true"
	c.mode = os.Getenv("MODE")
	c.model = os.Getenv("MODEL")
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
	if cfg.mode == "" {
		cfg.mode = "raw"
	}
	if cfg.mode != "agent" && cfg.mode != "dev" && cfg.mode != "raw" {
		log.Fatalf("invalid MODE environment variable: %s, must be one of: agent, dev, raw", cfg.mode)
	}
	if cfg.model == "" {
		cfg.model = "claude-sonnet-4"
	}
	model := tui.Initial(debugLogger, cfg.openRouterKey, runInBashDocker,
		tui.WithStaticMode("agent", agentPrompt),
		tui.WithStaticMode("dev", devPrompt),
		tui.WithStaticMode("raw", rawPrompt),
		tui.WithSetDefaultMode(cfg.mode),
		tui.WithSetDefaultModel(cfg.model),
	)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		log.Fatalf("error running program: %v", err)
	}
}
