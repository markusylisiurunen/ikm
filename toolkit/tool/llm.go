package tool

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

const (
	llmToolMaxFileSize     = 50 * 1024 * 1024
	llmToolMaxImageSize    = 1536 // 2*768 pixels: https://ai.google.dev/gemini-api/docs/image-understanding#technical-details-image
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

var _ llm.Tool = (*llmTool)(nil)

type llmTool struct {
	logger          logger.Logger
	openRouterToken string
	availableModels map[string]string
}

func NewLLM(openRouterToken string) *llmTool {
	return &llmTool{
		logger:          logger.NoOp(),
		openRouterToken: openRouterToken,
		availableModels: map[string]string{
			"claude-sonnet-4":  "anthropic/claude-sonnet-4",
			"gemini-2.5-flash": "google/gemini-2.5-flash-preview-05-20",
			"gemini-2.5-pro":   "google/gemini-2.5-pro-preview",
		},
	}
}

func (t *llmTool) SetLogger(logger logger.Logger) *llmTool {
	t.logger = logger
	return t
}

//go:embed llm.md
var llmToolDescription string

func (t *llmTool) Spec() (string, string, json.RawMessage) {
	return "llm", strings.TrimSpace(llmToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"model": {
				"type": "string",
				"description": "The LLM model to use. Must be one of the available models."
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
				"description": "Optional array of absolute file paths to images to include with the prompt. Images will be passed after the user prompt. Must be supported image formats."
			},
			"pdf_paths": {
				"type": "array",
				"items": {
					"type": "string"
				},
				"description": "Optional array of absolute file paths to PDF documents to include with the prompt. PDFs will be passed after the user prompt."
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
	// validate model
	model := gjson.Get(args, "model").String()
	if model == "" {
		t.logger.Error("llm tool called without model")
		return llmToolResult{Error: "model is required"}.result()
	}
	modelName := t.availableModels[model]
	if modelName == "" {
		t.logger.Error("llm tool called with invalid model: %s", model)
		return llmToolResult{Error: fmt.Sprintf("model %q is not available", model)}.result()
	}
	// validate user prompt
	userPrompt := gjson.Get(args, "user_prompt").String()
	if userPrompt == "" {
		t.logger.Error("llm tool called without user_prompt")
		return llmToolResult{Error: "user_prompt is required"}.result()
	}
	if len(userPrompt) > llmToolMaxPromptLength {
		t.logger.Error("llm tool called with user_prompt exceeding max length: %d", len(userPrompt))
		return llmToolResult{Error: fmt.Sprintf("user_prompt exceeds maximum length of %d characters", llmToolMaxPromptLength)}.result()
	}
	// optional system prompt
	systemPrompt := gjson.Get(args, "system_prompt").String()
	// process file paths
	imagePaths := gjson.Get(args, "image_paths").Array()
	pdfPaths := gjson.Get(args, "pdf_paths").Array()
	// build content parts
	t.logger.Debug("calling LLM with model %s, user prompt length %d, %d images and %d PDFs", model, len(userPrompt), len(imagePaths), len(pdfPaths))
	contentParts := llm.ContentParts{llm.NewTextContentPart(userPrompt)}
	// add images
	for _, imagePathValue := range imagePaths {
		imagePath := imagePathValue.String()
		if imagePath == "" {
			continue
		}
		imageContentPart, err := t.loadImageFile(imagePath)
		if err != nil {
			t.logger.Error("failed to load image %s: %s", imagePath, err.Error())
			return llmToolResult{Error: fmt.Sprintf("failed to load image %s: %s", imagePath, err.Error())}.result()
		}
		contentParts = append(contentParts, imageContentPart)
	}
	// add PDFs
	for _, pdfPathValue := range pdfPaths {
		pdfPath := pdfPathValue.String()
		if pdfPath == "" {
			continue
		}
		pdfContentPart, err := t.loadPDFFile(pdfPath)
		if err != nil {
			t.logger.Error("failed to load PDF %s: %s", pdfPath, err.Error())
			return llmToolResult{Error: fmt.Sprintf("failed to load PDF %s: %s", pdfPath, err.Error())}.result()
		}
		contentParts = append(contentParts, pdfContentPart)
	}
	// create LLM model and messages
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
	// call LLM
	events := llmModel.Stream(ctx, messages,
		llm.WithMaxTokens(16384),
		llm.WithMaxTurns(1),
		llm.WithTemperature(0.7),
	)
	responseMessages, _, err := llm.Rollup(events)
	if err != nil {
		t.logger.Error("LLM call failed: %s", err.Error())
		return llmToolResult{Error: fmt.Sprintf("LLM call failed: %s", err.Error())}.result()
	}
	if len(responseMessages) == 0 {
		t.logger.Error("no response received from LLM")
		return llmToolResult{Error: "no response received from LLM"}.result()
	}
	if responseMessages[0].Role != llm.RoleAssistant {
		t.logger.Error("unexpected response role: %s, expected %s", responseMessages[0].Role, llm.RoleAssistant)
		return llmToolResult{Error: fmt.Sprintf("unexpected response role: %s, expected %s", responseMessages[0].Role, llm.RoleAssistant)}.result()
	}
	answer := responseMessages[0].Content.Text()
	t.logger.Debug("LLM call completed successfully, response length: %d", len(answer))
	return llmToolResult{Answer: answer}.result()
}

func (t *llmTool) loadImageFile(imagePath string) (llm.ImageContentPart, error) {
	absPath, err := validatePath(imagePath)
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("invalid image path: %w", err)
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
	resizedData, mediaType, err := resizeImage(imageData, ext, llmToolMaxImageSize)
	if err != nil {
		return llm.ImageContentPart{}, fmt.Errorf("failed to process image: %w", err)
	}
	base64Data := base64.StdEncoding.EncodeToString(resizedData)
	return llm.NewImageContentPart(fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)), nil
}

func (t *llmTool) loadPDFFile(pdfPath string) (llm.FileContentPart, error) {
	absPath, err := validatePath(pdfPath)
	if err != nil {
		return llm.FileContentPart{}, fmt.Errorf("invalid PDF path: %w", err)
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

func resizeImage(imageData []byte, ext string, maxSize int) ([]byte, string, error) {
	img, format, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	shortestSide := min(height, width)
	if shortestSide <= maxSize {
		var mediaType string
		switch strings.ToLower(ext) {
		case ".jpg", ".jpeg":
			mediaType = "image/jpeg"
		case ".png":
			mediaType = "image/png"
		default:
			return nil, "", fmt.Errorf("unsupported image format: %s", ext)
		}
		return imageData, mediaType, nil
	}
	var newWidth, newHeight int
	if width < height {
		newWidth = maxSize
		newHeight = (height * maxSize) / width
	} else {
		newHeight = maxSize
		newWidth = (width * maxSize) / height
	}
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := range newHeight {
		for x := range newWidth {
			srcX := (x * width) / newWidth
			srcY := (y * height) / newHeight
			resized.Set(x, y, img.At(srcX, srcY))
		}
	}
	var buf bytes.Buffer
	var mediaType string
	switch format {
	case "jpeg":
		mediaType = "image/jpeg"
		err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 90})
	case "png":
		mediaType = "image/png"
		err = png.Encode(&buf, resized)
	default:
		mediaType = "image/jpeg"
		err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode resized image: %w", err)
	}
	return buf.Bytes(), mediaType, nil
}
