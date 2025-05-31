package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/markusylisiurunen/ikm/internal/logger"
	"github.com/markusylisiurunen/ikm/toolkit/llm"
	"github.com/tidwall/gjson"
)

const (
	fsToolMaxFileSize  = 10 * 1024 * 1024
	fsToolMaxFileCount = 10000
)

type fsToolResult struct {
	Ok      bool     `json:"ok"`
	Error   string   `json:"error,omitzero"`
	Files   []string `json:"files,omitzero"`
	Content string   `json:"content,omitzero"`
}

func (r fsToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

var fsToolDescription = strings.TrimSpace(`
Perform file system operations within the current working directory.

Operations:
- list: List all files in the current working directory tracked by git (cached and untracked, excluding ignored files)
- read: Read the contents of a file
- write: Create or overwrite a file with new content (creates parent directories if needed)
- remove: Delete a file
`)

var _ llm.Tool = (*fsTool)(nil)

type fsTool struct {
	logger logger.Logger
}

func NewFS() *fsTool {
	return &fsTool{
		logger: logger.NoOp(),
	}
}

func (t *fsTool) SetLogger(logger logger.Logger) *fsTool {
	t.logger = logger
	return t
}

func (t *fsTool) Spec() (string, string, json.RawMessage) {
	return "fs", fsToolDescription, json.RawMessage(`{
		"type": "object",
		"properties": {
			"op": {
				"type": "string",
				"enum": ["list", "read", "write", "remove"],
				"description": "The operation to perform on the file system"
			},
			"path": {
				"type": "string",
				"description": "The relative file path for the operation (required for 'read', 'write', and 'remove' operations)"
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file (required for 'write' operation)"
			}
		},
		"required": ["op"]
	}`)
}

func (t *fsTool) Call(ctx context.Context, args string) (string, error) {
	var (
		operation = gjson.Get(args, "op").String()
		result    string
		err       error
	)
	switch operation {
	case "list":
		result, err = t.list(ctx, args)
	case "read":
		result, err = t.read(ctx, args)
	case "write":
		result, err = t.write(ctx, args)
	case "remove":
		result, err = t.remove(ctx, args)
	default:
		err = fmt.Errorf("unknown operation: %s", operation)
	}
	if err != nil {
		t.logger.Error("file system operation (%s) failed: %s", operation, err.Error())
		return fsToolResult{Ok: false, Error: err.Error()}.result()
	}
	t.logger.Debug("file system operation (%s) for path %q succeeded", operation, gjson.Get(args, "path").String())
	return result, nil
}

func (t *fsTool) list(ctx context.Context, _ string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--cached", "--others", "--exclude-standard")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("command failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())
	}
	if err != nil {
		return "", fmt.Errorf("command failed: %s", err.Error())
	}
	files := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(files) > fsToolMaxFileCount {
		return "", fmt.Errorf("too many files to list: %d exceeds limit of %d", len(files), fsToolMaxFileCount)
	}
	return fsToolResult{Ok: true, Files: files}.result()
}

func (t *fsTool) read(ctx context.Context, args string) (string, error) {
	filePath := gjson.Get(args, "path").String()
	if filePath == "" {
		return "", fmt.Errorf("path parameter is required for read operation")
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat file: %s", err.Error())
	}
	if fileInfo.Size() > fsToolMaxFileSize {
		return "", fmt.Errorf("file size exceeds limit of %d bytes", fsToolMaxFileSize)
	}
	cmd := exec.CommandContext(ctx, "cat", filePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("command failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())
	}
	if err != nil {
		return "", fmt.Errorf("command failed: %s", err.Error())
	}
	return fsToolResult{Ok: true, Content: stdout.String()}.result()
}

func (t *fsTool) write(_ context.Context, args string) (string, error) {
	filePath := gjson.Get(args, "path").String()
	if filePath == "" {
		return "", fmt.Errorf("path parameter is required for write operation")
	}
	content := gjson.Get(args, "content").String()
	if content == "" {
		return "", fmt.Errorf("content parameter is required for write operation")
	}
	if len(content) > fsToolMaxFileSize {
		return "", fmt.Errorf("content size exceeds limit of %d bytes", fsToolMaxFileSize)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %s", err.Error())
	}
	cleanPath := filepath.Clean(filePath)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %s", err.Error())
	}
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("path must be within the current directory")
	}
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %s", err.Error())
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %s", err.Error())
	}
	return fsToolResult{Ok: true}.result()
}

func (t *fsTool) remove(ctx context.Context, args string) (string, error) {
	filePath := gjson.Get(args, "path").String()
	if filePath == "" {
		return "", fmt.Errorf("path parameter is required for remove operation")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %s", err.Error())
	}
	cleanPath := filepath.Clean(filePath)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %s", err.Error())
	}
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("path must be within the current directory")
	}
	cmd := exec.CommandContext(ctx, "rm", absPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("command failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())
	}
	if err != nil {
		return "", fmt.Errorf("command failed: %s", err.Error())
	}
	return fsToolResult{Ok: true}.result()
}
