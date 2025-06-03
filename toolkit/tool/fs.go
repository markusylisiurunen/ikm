package tool

import (
	"bytes"
	"context"
	_ "embed"
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

// fs_list -----------------------------------------------------------------------------------------

const (
	fsListToolMaxFileCount = 10_000
)

var _ llm.Tool = (*fsListTool)(nil)

type fsListToolResult struct {
	Error string   `json:"error,omitzero"`
	Files []string `json:"files,omitzero"`
}

func (r fsListToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

type fsListTool struct {
	logger logger.Logger
}

func NewFSList() *fsListTool {
	return &fsListTool{logger.NoOp()}
}

func (t *fsListTool) SetLogger(logger logger.Logger) *fsListTool {
	t.logger = logger
	return t
}

//go:embed fs_list.md
var fsListToolDescription string

func (t *fsListTool) Spec() (string, string, json.RawMessage) {
	return "fs_list", strings.TrimSpace(fsListToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The absolute path to the directory to list"
			}
		},
		"required": ["path"]
	}`)
}

func (t *fsListTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Error("fs_list tool called with invalid JSON arguments")
		return fsListToolResult{Error: "invalid JSON arguments"}.result()
	}
	// validate the provided path
	path := gjson.Get(args, "path").String()
	absPath, err := validatePath(path)
	if err != nil {
		t.logger.Error("fs_list operation failed: %s", err.Error())
		return fsListToolResult{Error: err.Error()}.result()
	}
	// check if the path exists and is a directory
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		t.logger.Error("fs_list operation failed: %s", err.Error())
		return fsListToolResult{Error: fmt.Sprintf("failed to stat path: %s", err.Error())}.result()
	}
	if !fileInfo.IsDir() {
		t.logger.Error("fs_list operation failed: path is not a directory")
		return fsListToolResult{Error: "path must be a directory"}.result()
	}
	// change to the specified directory and run git ls-files
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = absPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		t.logger.Error("fs_list operation failed: %s", stderr.String())
		return fsListToolResult{Error: fmt.Sprintf("command failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())}.result()
	}
	if err != nil {
		t.logger.Error("fs_list operation failed: %s", err.Error())
		return fsListToolResult{Error: fmt.Sprintf("command failed: %s", err.Error())}.result()
	}
	// process the output
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		t.logger.Debug("fs_list operation succeeded: no files found")
		return fsListToolResult{Files: []string{}}.result()
	}
	files := strings.Split(output, "\n")
	if len(files) > fsListToolMaxFileCount {
		err := fmt.Errorf("too many files to list: %d exceeds limit of %d", len(files), fsListToolMaxFileCount)
		t.logger.Error("fs_list operation failed: %s", err.Error())
		return fsListToolResult{Error: err.Error()}.result()
	}
	// convert relative paths to absolute paths
	absFiles := make([]string, 0, len(files))
	for _, file := range files {
		if file != "" {
			absFile := filepath.Join(absPath, file)
			absFiles = append(absFiles, absFile)
		}
	}
	t.logger.Debug("fs_list operation succeeded: found %d files", len(absFiles))
	return fsListToolResult{Files: absFiles}.result()
}

// fs_read -----------------------------------------------------------------------------------------

const (
	fsReadToolMaxFileSize = 10 * 1024 * 1024
)

var _ llm.Tool = (*fsReadTool)(nil)

type fsReadToolResult struct {
	Error   string `json:"error,omitzero"`
	Content string `json:"content,omitzero"`
}

func (r fsReadToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

type fsReadTool struct {
	logger logger.Logger
}

func NewFSRead() *fsReadTool {
	return &fsReadTool{logger.NoOp()}
}

func (t *fsReadTool) SetLogger(logger logger.Logger) *fsReadTool {
	t.logger = logger
	return t
}

//go:embed fs_read.md
var fsReadToolDescription string

func (t *fsReadTool) Spec() (string, string, json.RawMessage) {
	return "fs_read", strings.TrimSpace(fsReadToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The absolute path to the file to read"
			},
			"offset": {
				"type": "number",
				"description": "The line number to start reading from (1-based). Only provide if the file is too large to read at once"
			},
			"limit": {
				"type": "number",
				"description": "The number of lines to read. Only provide if the file is too large to read at once"
			}
		},
		"required": ["path"]
	}`)
}

func (t *fsReadTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Error("fs_read tool called with invalid JSON arguments")
		return fsReadToolResult{Error: "invalid JSON arguments"}.result()
	}
	// validate the provided path and parameters
	filePath := gjson.Get(args, "path").String()
	offset := gjson.Get(args, "offset").Int()
	limit := gjson.Get(args, "limit").Int()
	absPath, err := validatePath(filePath)
	if err != nil {
		t.logger.Error("fs_read operation failed: %s", err.Error())
		return fsReadToolResult{Error: err.Error()}.result()
	}
	// check if the file exists and is readable
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		t.logger.Error("fs_read operation failed: %s", err.Error())
		return fsReadToolResult{Error: fmt.Sprintf("failed to stat file: %s", err.Error())}.result()
	}
	if fileInfo.Size() > fsReadToolMaxFileSize {
		err := fmt.Errorf("file size exceeds limit of %d bytes", fsReadToolMaxFileSize)
		t.logger.Error("fs_read operation failed: %s", err.Error())
		return fsReadToolResult{Error: err.Error()}.result()
	}
	// read the file using appropriate command based on offset and limit
	var cmd *exec.Cmd
	if offset > 0 && limit > 0 {
		cmd = exec.CommandContext(ctx, "sed", "-n", fmt.Sprintf("%d,%dp", offset, offset+limit-1), absPath)
	} else if offset > 0 {
		cmd = exec.CommandContext(ctx, "tail", "-n", fmt.Sprintf("+%d", offset), absPath)
	} else if limit > 0 {
		cmd = exec.CommandContext(ctx, "head", "-n", fmt.Sprintf("%d", limit), absPath)
	} else {
		cmd = exec.CommandContext(ctx, "cat", absPath)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		t.logger.Error("fs_read operation failed: %s", stderr.String())
		return fsReadToolResult{Error: fmt.Sprintf("command failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())}.result()
	}
	if err != nil {
		t.logger.Error("fs_read operation failed: %s", err.Error())
		return fsReadToolResult{Error: fmt.Sprintf("command failed: %s", err.Error())}.result()
	}
	// add line numbers to the output
	content := stdout.String()
	if content != "" {
		// preserve whether the original content had a trailing newline
		hasTrailingNewline := strings.HasSuffix(content, "\n")
		lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
		numberedLines := make([]string, len(lines))
		startLine := 1
		if offset > 0 {
			startLine = int(offset)
		}
		for i, line := range lines {
			numberedLines[i] = fmt.Sprintf("%6d\t%s", startLine+i, line)
		}
		content = strings.Join(numberedLines, "\n")
		if hasTrailingNewline {
			content += "\n"
		}
	}
	t.logger.Debug("fs_read operation for path %q succeeded", filePath)
	return fsReadToolResult{Content: content}.result()
}

// fs_write ----------------------------------------------------------------------------------------

const (
	fsWriteToolMaxFileSize = 10 * 1024 * 1024
)

var _ llm.Tool = (*fsWriteTool)(nil)

type fsWriteToolResult struct {
	Error string `json:"error,omitzero"`
}

func (r fsWriteToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

type fsWriteTool struct {
	logger logger.Logger
}

func NewFSWrite() *fsWriteTool {
	return &fsWriteTool{logger.NoOp()}
}

func (t *fsWriteTool) SetLogger(logger logger.Logger) *fsWriteTool {
	t.logger = logger
	return t
}

//go:embed fs_write.md
var fsWriteToolDescription string

func (t *fsWriteTool) Spec() (string, string, json.RawMessage) {
	return "fs_write", strings.TrimSpace(fsWriteToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The absolute path to the file to write"
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file"
			}
		},
		"required": ["path", "content"]
	}`)
}

func (t *fsWriteTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Error("fs_write tool called with invalid JSON arguments")
		return fsWriteToolResult{Error: "invalid JSON arguments"}.result()
	}
	// validate the provided path and content
	filePath := gjson.Get(args, "path").String()
	content := gjson.Get(args, "content").String()
	if content == "" {
		t.logger.Error("fs_write operation failed: content parameter is required")
		return fsWriteToolResult{Error: "content parameter is required"}.result()
	}
	if len(content) > fsWriteToolMaxFileSize {
		err := fmt.Errorf("content size exceeds limit of %d bytes", fsWriteToolMaxFileSize)
		t.logger.Error("fs_write operation failed: %s", err.Error())
		return fsWriteToolResult{Error: err.Error()}.result()
	}
	absPath, err := validatePath(filePath)
	if err != nil {
		t.logger.Error("fs_write operation failed: %s", err.Error())
		return fsWriteToolResult{Error: err.Error()}.result()
	}
	// make sure the parent directory exists
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.logger.Error("fs_write operation failed: %s", err.Error())
		return fsWriteToolResult{Error: fmt.Sprintf("failed to create parent directories: %s", err.Error())}.result()
	}
	// write the content to the file
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.logger.Error("fs_write operation failed: %s", err.Error())
		return fsWriteToolResult{Error: fmt.Sprintf("failed to write file: %s", err.Error())}.result()
	}
	t.logger.Debug("fs_write operation for path %q succeeded", filePath)
	return fsWriteToolResult{}.result()
}

// fs_replace --------------------------------------------------------------------------------------

const (
	fsReplaceToolMaxFileSize = 10 * 1024 * 1024
)

var _ llm.Tool = (*fsReplaceTool)(nil)

type fsReplaceToolResult struct {
	Error string `json:"error,omitzero"`
}

func (r fsReplaceToolResult) result() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	return string(b), nil
}

type fsReplaceTool struct {
	logger logger.Logger
}

func NewFSReplace() *fsReplaceTool {
	return &fsReplaceTool{logger.NoOp()}
}

func (t *fsReplaceTool) SetLogger(logger logger.Logger) *fsReplaceTool {
	t.logger = logger
	return t
}

//go:embed fs_replace.md
var fsReplaceToolDescription string

func (t *fsReplaceTool) Spec() (string, string, json.RawMessage) {
	return "fs_replace", strings.TrimSpace(fsReplaceToolDescription), json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The absolute path to the file to modify"
			},
			"old_string": {
				"type": "string",
				"description": "The text to replace"
			},
			"new_string": {
				"type": "string",
				"description": "The text to replace it with (must be different from old_string)"
			},
			"replace_all": {
				"type": "boolean",
				"description": "Replace all occurrences of old_string (default false)"
			}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *fsReplaceTool) Call(ctx context.Context, args string) (string, error) {
	if !gjson.Valid(args) {
		t.logger.Error("fs_replace tool called with invalid JSON arguments")
		return fsReplaceToolResult{Error: "invalid JSON arguments"}.result()
	}
	// validate the provided path and parameters
	filePath := gjson.Get(args, "path").String()
	oldStr := gjson.Get(args, "old_string").String()
	newStr := gjson.Get(args, "new_string").String()
	replaceAll := gjson.Get(args, "replace_all").Bool()
	if oldStr == "" {
		t.logger.Error("fs_replace operation failed: old_string parameter is required")
		return fsReplaceToolResult{Error: "old_string parameter is required"}.result()
	}
	if oldStr == newStr {
		t.logger.Error("fs_replace operation failed: old_string and new_string must be different")
		return fsReplaceToolResult{Error: "old_string and new_string must be different"}.result()
	}
	absPath, err := validatePath(filePath)
	if err != nil {
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: err.Error()}.result()
	}
	// check if the file exists and is readable
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: fmt.Sprintf("failed to stat file: %s", err.Error())}.result()
	}
	if fileInfo.Size() > fsReplaceToolMaxFileSize {
		err := fmt.Errorf("file size exceeds limit of %d bytes", fsReplaceToolMaxFileSize)
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: err.Error()}.result()
	}
	// read the file content
	content, err := os.ReadFile(absPath)
	if err != nil {
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: fmt.Sprintf("failed to read file: %s", err.Error())}.result()
	}
	contentStr := string(content)
	// replace the old string with the new string (if valid)
	if !replaceAll {
		occurrences := strings.Count(contentStr, oldStr)
		if occurrences == 0 {
			err := fmt.Errorf("old_string not found in file")
			t.logger.Error("fs_replace operation failed: %s", err.Error())
			return fsReplaceToolResult{Error: err.Error()}.result()
		}
		if occurrences > 1 {
			err := fmt.Errorf("old_string appears %d times in file, must be unique for single replacement", occurrences)
			t.logger.Error("fs_replace operation failed: %s", err.Error())
			return fsReplaceToolResult{Error: err.Error()}.result()
		}
	}
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(contentStr, oldStr, newStr)
	} else {
		newContent = strings.Replace(contentStr, oldStr, newStr, 1)
	}
	// write the modified content back to the file
	if len(newContent) > fsReplaceToolMaxFileSize {
		err := fmt.Errorf("new content size exceeds limit of %d bytes", fsReplaceToolMaxFileSize)
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: err.Error()}.result()
	}
	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		t.logger.Error("fs_replace operation failed: %s", err.Error())
		return fsReplaceToolResult{Error: fmt.Sprintf("failed to write file: %s", err.Error())}.result()
	}
	t.logger.Debug("fs_replace operation for path %q succeeded", filePath)
	return fsReplaceToolResult{}.result()
}

// helpers -----------------------------------------------------------------------------------------

func validatePath(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("path parameter is required")
	}
	cleanPath := filepath.Clean(filePath)
	// convert to absolute path if not already absolute
	var absPath string
	var err error
	if filepath.IsAbs(cleanPath) {
		absPath = cleanPath
	} else {
		absPath, err = filepath.Abs(cleanPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute path: %s", err.Error())
		}
	}
	// get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %s", err.Error())
	}
	// ensure the absolute path is within the current working directory
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil {
		return "", fmt.Errorf("failed to determine relative path: %s", err.Error())
	}
	// check if the path tries to escape the current working directory
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be within the current working directory")
	}
	return absPath, nil
}
