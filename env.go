package main

import (
	"fmt"
	"os"
	"slices"
	"time"
)

type Mode string

const (
	ModeAgent Mode = "agent"
	ModeDev   Mode = "dev"
	ModeRaw   Mode = "raw"
)

type Env struct {
	Debug         bool
	LogsDir       string
	LogsSuffix    string
	Mode          Mode
	OpenRouterKey string
	Stream        bool
}

var env Env

func init() {
	env = Env{
		Debug:         os.Getenv("DEBUG") == "1" || os.Getenv("DEBUG") == "true",
		LogsDir:       ".logs",
		LogsSuffix:    time.Now().Format("2006-01-02-15:04:05"),
		Mode:          Mode(os.Getenv("MODE")),
		OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"),
		Stream:        os.Getenv("STREAM") == "1" || os.Getenv("STREAM") == "true",
	}
	knownModes := []Mode{ModeAgent, ModeDev, ModeRaw}
	if !slices.Contains(knownModes, env.Mode) {
		panic(fmt.Sprintf("unknown mode: %s", env.Mode))
	}
}
