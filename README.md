# ikm

A terminal-based AI coding assistant that acts as your knowledgeable pair programming partner.

## Features

- Code explanations, generation, debugging, and architectural guidance
- Multiple modes: `agent`, `dev`, `raw`
- Configurable tools: `bash`, `fs`, `llm`, `task`, `think`
- Secure Docker-based command execution
- Clean terminal UI with Bubble Tea

## Installation

```bash
go install github.com/markusylisiurunen/ikm/cmd/ikm@latest
```

## Getting started

```bash
export OPENROUTER_KEY=your_key_here
go run cmd/ikm/*.go
```

## Usage

```bash
ikm --mode dev --model claude-sonnet-4
ikm --no-tool-task
```
