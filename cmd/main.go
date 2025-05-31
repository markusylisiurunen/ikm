package main

import (
	_ "embed"
	"fmt"
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

func main() {
	if err := buildBashDockerIfNeeded(); err != nil {
		fmt.Printf("error building docker image: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(".logs", 0755); err != nil {
		panic(fmt.Sprintf("error creating debug folder: %v", err))
	}
	debugLogFile := "debug-" + time.Now().Format("2006-01-02T15:04:05") + ".log"
	f, err := os.OpenFile(".logs/"+debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(fmt.Sprintf("error opening log file: %v", err))
	}
	defer f.Close() //nolint:errcheck
	logger := logger.New(f)
	logger.SetEnabled(true)
	logger.SetLevel("debug")
	model := tui.Initial(logger, os.Getenv("OPENROUTER_API_KEY"), runInBashDocker,
		tui.WithStaticMode("agent", agentPrompt),
		tui.WithStaticMode("dev", devPrompt),
		tui.WithStaticMode("raw", rawPrompt),
		tui.WithSetDefaultMode("dev"),
	)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}
