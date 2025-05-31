package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"slices"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

const (
	llmToolMaxFileSize     = 50 * 1024 * 1024
	llmToolMaxPromptLength = 32 * 1024
	llmToolTimeout         = 5 * time.Minute
)

type llmToolResult struct {
	Error  string `json:"error,omitzero"`
	Answer string `json:"answer,omitzero"`
}

func (r llmToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var llmToolDescription = strings.TrimSpace(`
Call an LLM with a prompt and optional image or PDF files. This tool provides direct access to language models for analysis, generation, and reasoning tasks.

Supported image formats: .jpg, .jpeg, .png
Maximum prompt length: 32KB

Use this tool when you need to:
- Analyze or understand content
- Generate text, code, or documentation
- Reason about complex problems
- Process images or visual content
- Get expert knowledge on specific topics

The following LLMs are available for use:
- google/gemini-2.5-flash-preview-05-20: A fast but very capable model, especially great for visual understanding and PDF parsing.
- anthropic/claude-sonnet-4: A highly capable model for in-depth analysis and reasoning tasks.
- google/gemini-2.5-pro-preview: The most capable model, suitable for complex reasoning, coding, and analysis tasks.
`)

var _ llm.Tool = (*llmTool)(nil)

type llmTool struct {
	logger          logger.Logger
	openRouterToken string
	availableModels []string
}

func NewLLM(openRouterToken string) *llmTool {
	return &llmTool{
		logger:          logger.NoOp(),
		openRouterToken: openRouterToken,
		availableModels: []string{
			"google/gemini-2.5-flash-preview-05-20",
			"anthropic/claude-sonnet-4",
			"google/gemini-2.5-pro-preview",
		},
	}
}

func (t *llmTool) SetLogger(logger logger.Logger) *llmTool {
	t.logger = logger
	return t
}

func (t *llmTool) Spec() (string, string, json.RawMessage) {
	return "llm", llmToolDescription, json.RawMessage(`{
		"type": "object",
		"properties": {
			"model": {
				"type": "string",
				"description": "The LLM model to use for the call. Available models: google/gemini-2.5-flash-preview-05-20, anthropic/claude-sonnet-4, google/gemini-2.5-pro-preview"
			},
			"system_prompt": {
				"type": "string",
				"description": "Optional system prompt to set the context for the LLM. If not provided, no system prompt will be used."
			},
			"user_prompt": {
				"type": "string",
				"description": "The user prompt to send to the LLM. This is the main input (the first user message) for the LLM."
			},
			"image_paths": {
				"type": "array",
				"items": {
					"type": "string"
				},
				"description": "Optional array of file paths to images to include with the prompt. These will be passed after the user prompt in the LLM call. Supported formats: .jpg, .jpeg, .png."
			},
			"pdf_paths": {
				"type": "array",
				"items": {
					"type": "string"
				},
				"description": "Optional array of file paths to PDF documents to include with the prompt. These will be passed after the user prompt in the LLM call. Supported formats: .pdf."
			}
		},
		"required": ["model", "user_prompt"]
	}`)
}

func (t *llmTool) Call(ctx context.Context, args string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, llmToolTimeout)
	defer cancel()
	if !gjson.Valid(args) {
		t.logger.Error("llm tool called with invalid JSON arguments")
		return llmToolResult{Error: "invalid JSON arguments"}.result()
	}
	model := gjson.Get(args, "model").String()
	if model == "" {
		return llmToolResult{Error: "model cannot be empty"}.result()
	}
	modelValid := slices.Contains(t.availableModels, model)
	if !modelValid {
		return llmToolResult{Error: fmt.Sprintf("model %q is not available", model)}.result()
	}
	systemPrompt := gjson.Get(args, "system_prompt").String()
	userPrompt := gjson.Get(args, "user_prompt").String()
	if userPrompt == "" {
		return llmToolResult{Error: "user_prompt cannot be empty"}.result()
	}
	if len(userPrompt) > llmToolMaxPromptLength {
		return llmToolResult{Error: fmt.Sprintf("user_prompt exceeds maximum length of %d characters", llmToolMaxPromptLength)}.result()
	}
	imagePaths := gjson.Get(args, "image_paths").Array()
	pdfPaths := gjson.Get(args, "pdf_paths").Array()
	t.logger.Debug("calling LLM with model %s, user prompt length %d, %d images and %d PDFs", model, len(userPrompt), len(imagePaths), len(pdfPaths))
	contentParts := llm.ContentParts{llm.NewTextContentPart(userPrompt)}
	for _, imagePathValue := range imagePaths {
		imagePath := imagePathValue.String()
		if imagePath == "" {
			continue
		}
		imageContentPart, err := t.loadImageFile(imagePath)
		if err != nil {
			return llmToolResult{Error: fmt.Sprintf("failed to load image %s: %s", imagePath, err.Error())}.result()
		}
		contentParts = append(contentParts, imageContentPart)
	}
	for _, pdfPathValue := range pdfPaths {
		pdfPath := pdfPathValue.String()
		if pdfPath == "" {
			continue
		}
		pdfContentPart, err := t.loadPDFFile(pdfPath)
		if err != nil {
			return llmToolResult{Error: fmt.Sprintf("failed to load PDF %s: %s", pdfPath, err.Error())}.result()
		}
		contentParts = append(contentParts, pdfContentPart)
	}
	llmModel := llm.NewOpenRouter(t.logger, t.openRouterToken, model)
	messages := []llm.Message{}
	if systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: llm.ContentParts{llm.NewTextContentPart(systemPrompt)},
		})
	}
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: contentParts,
	})
	events := llmModel.Stream(ctx, messages,
		llm.WithMaxTokens(8192),
		llm.WithMaxTurns(1),
		llm.WithTemperature(0.7),
	)
	responseMessages, _, err := llm.Rollup(events)
	if err != nil {
		t.logger.Error("LLM call failed: %s", err.Error())
		return llmToolResult{Error: fmt.Sprintf("LLM call failed: %s", err.Error())}.result()
	}
	if len(responseMessages) == 0 {
		return llmToolResult{Error: "no response received from LLM"}.result()
	}
	if responseMessages[0].Role != llm.RoleAssistant {
		return llmToolResult{Error: fmt.Sprintf("unexpected response role: %s, expected %s", responseMessages[0].Role, llm.RoleAssistant)}.result()
	}
	answer := responseMessages[0].Content.Text()
	t.logger.Debug("LLM call completed successfully, response length: %d", len(answer))
	return llmToolResult{Answer: answer}.result()
}

func (t *llmTool) loadImageFile(imagePath string) (llm.ImageContentPart, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("failed to get current directory: %w", err)
	}
	cleanPath := filepath.Clean(imagePath)
	if strings.Contains(cleanPath, "..") {
		return llm.ImageContentPart{}, fmt.Errorf("image path must not contain '..' sequences")
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return llm.ImageContentPart{}, fmt.Errorf("image path must be within the current directory")
	}
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("failed to stat image file: %w", err)
	}
	if fileInfo.Size() > llmToolMaxFileSize {
		return llm.ImageContentPart{}, fmt.Errorf("image file size exceeds limit of %d bytes", llmToolMaxFileSize)
	}
	imageData, err := os.ReadFile(absPath)
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("failed to read image file: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	var mediaType string
	switch ext {
	case ".jpg", ".jpeg":
		mediaType = "image/jpeg"
	case ".png":
		mediaType = "image/png"
	default:
		return llm.ImageContentPart{}, fmt.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png)", ext)
	}
	base64Data := base64.StdEncoding.EncodeToString(imageData)
	return llm.NewImageContentPart(fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)), nil
}

func (t *llmTool) loadPDFFile(pdfPath string) (llm.FileContentPart, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return llm.FileContentPart{}, fmt.Errorf("failed to get current directory: %w", err)
	}
	cleanPath := filepath.Clean(pdfPath)
	if strings.Contains(cleanPath, "..") {
		return llm.FileContentPart{}, fmt.Errorf("PDF path must not contain '..' sequences")
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return llm.FileContentPart{}, fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return llm.FileContentPart{}, fmt.Errorf("PDF path must be within the current directory")
	}
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return llm.FileContentPart{}, fmt.Errorf("failed to stat PDF file: %w", err)
	}
	if fileInfo.Size() > llmToolMaxFileSize {
		return llm.FileContentPart{}, fmt.Errorf("PDF file size exceeds limit of %d bytes", llmToolMaxFileSize)
	}
	fileData, err := os.ReadFile(absPath)
	if err != nil {
		return llm.FileContentPart{}, fmt.Errorf("failed to read PDF file: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	var mediaType string
	switch ext {
	case ".pdf":
		mediaType = "application/pdf"
	default:
		return llm.FileContentPart{}, fmt.Errorf("unsupported file format: %s (supported: .pdf)", ext)
	}
	fileName := filepath.Base(absPath)
	base64Data := base64.StdEncoding.EncodeToString(fileData)
	return llm.NewFileContentPart(fileName, fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)), nil
}
