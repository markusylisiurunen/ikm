package main

import (
	_ "embed"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if err := buildBashDockerIfNeeded(); err != nil {
		fmt.Printf("error building docker image: %v\n", err)
		os.Exit(1)
	}
	program := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}
