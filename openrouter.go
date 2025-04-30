package main

import (
	_ "embed"
)

//go:embed prompts/agent.txt
var agentSystemPrompt string

//go:embed prompts/dev.txt
var devSystemPrompt string

type OpenRouterModel struct {
	ID        string
	Name      string
	Cacheable bool
	Thinks    bool
	UsesTools bool
}

var (
	Model_Claude_3_7_Sonnet         = OpenRouterModel{"anthropic/claude-3.7-sonnet", "claude", true, true, true}
	Model_GPT_4_1                   = OpenRouterModel{"openai/gpt-4.1", "gpt-4", false, true, true}
	Model_Gemini_2_5_Flash          = OpenRouterModel{"google/gemini-2.5-flash-preview", "gemini-flash", false, false, true}
	Model_Gemini_2_5_Flash_Thinking = OpenRouterModel{"google/gemini-2.5-flash-preview:thinking", "gemini-flash-thinking", false, true, true}
	Model_Gemini_2_5_Pro            = OpenRouterModel{"google/gemini-2.5-pro-preview-03-25", "gemini-pro", true, true, true}
	Model_Grok_3                    = OpenRouterModel{"x-ai/grok-3-beta", "grok", false, true, false}
	Model_Grok_3_Mini               = OpenRouterModel{"x-ai/grok-3-mini-beta", "grok-mini", false, true, false}
	Model_o3                        = OpenRouterModel{"openai/o3", "o3", false, true, true}
	Model_o4_Mini_High              = OpenRouterModel{"openai/o4-mini-high", "o4-mini", false, true, true}
)

var (
	defaultModel    = Model_Gemini_2_5_Pro
	availableModels = []OpenRouterModel{
		Model_Claude_3_7_Sonnet,
		Model_GPT_4_1,
		Model_Gemini_2_5_Flash,
		Model_Gemini_2_5_Flash_Thinking,
		Model_Gemini_2_5_Pro,
		Model_Grok_3,
		Model_Grok_3_Mini,
		Model_o3,
		Model_o4_Mini_High,
	}
)

const (
	defaultOpenRouterMaxTokens   = 32_768
	defaultOpenRouterTemperature = 0.2
)
