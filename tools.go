package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Tool interface {
	Definition() OpenRouterRequest_Tool
	Execute(ctx context.Context, args string) (string, error)
}

// bash tool ---------------------------------------------------------------------------------------

type bashTool struct{}

func (b bashTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "bash",
			Description: strings.Join([]string{
				"Execute a bash command in a secure Ubuntu Noble sandbox.",
				"Constraints: The sandbox has a read-only filesystem (files cannot be modified) and network access is disabled.",
				"Permissions: You can run any command that only reads from the filesystem and does not require network access.",
				"Examples: `ls`, `nl -ba -w1`, `rg`, `sed` (for viewing), `git status`, `git diff`, `git log`.",
				"Prohibited: Commands attempting to modify files (`sed -i`, `mv`, `rm`, `git commit`) or access the network (`curl`, `wget`, `git clone`) will fail.",
				"Always prefer using line-numbering commands (e.g., `nl -ba -w1 <file> | sed -n <start>,<end>p`) when inspecting files you might edit later with `patch`.",
				"Important: Refer to the instructions for this tool in the system prompt.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"cmd": {
						"type": "string",
						"description": "Bash command to execute."
					}
				},
				"required": ["cmd"]
			}`),
		},
	}
}

func (b bashTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	code, stdout, stderr, err := runInBashDocker(ctx, params.Cmd)
	if err != nil {
		return "", fmt.Errorf("error executing command: %w", err)
	}
	out, err := json.Marshal(struct {
		Code   int    `json:"code"`
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}{
		Code:   code,
		Stdout: strings.TrimSpace(stdout),
		Stderr: strings.TrimSpace(stderr),
	})
	if err != nil {
		return "", fmt.Errorf("error marshalling output: %w", err)
	}
	return string(out), nil
}

// write tool --------------------------------------------------------------------------------------

type writeTool struct{}

func (w writeTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "write",
			Description: strings.Join([]string{
				"Creates a new file or completely overwrites an existing file with the provided content.",
				"Parent directories are created if needed. Use this for creating new files or replacing entire file contents.",
				"For modifying parts of an existing file, use the 'patch' tool instead.",
				"Important: Refer to the instructions for this tool in the system prompt.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file": {
						"type": "string",
						"description": "File to write (new or existing)."
					},
					"content": {
						"type": "string",
						"description": "Content to write to the file."
					}
				},
				"required": ["file", "content"]
			}`),
		},
	}
}

func (w writeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	if params.File == "" || params.Content == "" {
		return "", fmt.Errorf("file name or content is empty")
	}
	if ok, err := w.isPathWithinRoot(params.File); err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	} else if !ok {
		return "", fmt.Errorf("path %s is not within the root directory", params.File)
	}
	cleanPath := filepath.Clean(params.File)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return "", fmt.Errorf("error creating directory: %w", err)
	}
	if err := os.WriteFile(cleanPath, []byte(params.Content), 0644); err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}
	return "", nil
}

func (w writeTool) isPathWithinRoot(targetPath string) (bool, error) {
	rootDir, err := filepath.Abs(".")
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}
	if !strings.HasSuffix(rootDir, string(filepath.Separator)) {
		rootDir += string(filepath.Separator)
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of target: %w", err)
	}
	if !strings.HasPrefix(targetAbs, rootDir) {
		return false, nil
	}
	cleanPath := filepath.Clean(targetPath)
	if targetPath != cleanPath && strings.Contains(targetPath, "..") {
		return false, errors.New("path contains suspicious directory traversal elements")
	}
	return true, nil
}

// patch tool --------------------------------------------------------------------------------------

type patchTool struct{}

func (p patchTool) Definition() OpenRouterRequest_Tool {
	return OpenRouterRequest_Tool{
		Type: "function",
		Function: &OpenRouterRequest_Tool_Function{
			Name: "patch",
			Description: strings.Join([]string{
				"Replaces a specific line range (1-based index) in a file with new content.",
				"Preferred over 'write' for partial modifications.",
				"Important: Refer to the instructions for this tool in the system prompt.",
			}, " "),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file": {
						"type": "string",
						"description": "File to patch."
					},
					"range_start": {
						"type": "number",
						"description": "Start of the line range to replace."
					},
					"range_end": {
						"type": "number",
						"description": "End of the line range to replace."
					},
					"content": {
						"type": "string",
						"description": "Content to replace the line range with."
					}
				},
				"required": ["file", "range_start", "range_end", "content"]
			}`),
		},
	}
}

func (p patchTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		File    string `json:"file"`
		Start   int    `json:"range_start"`
		End     int    `json:"range_end"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling arguments: %w", err)
	}
	if ok, err := p.isPathWithinRoot(params.File); err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	} else if !ok {
		return "", fmt.Errorf("path %s is not within the root directory", params.File)
	}
	cleanPath := filepath.Clean(params.File)
	contentBytes, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", fmt.Errorf("error reading file %q: %w", cleanPath, err)
	}
	lines := strings.Split(string(contentBytes), "\n")
	startIdx := params.Start - 1
	endIdx := params.End
	if params.Start < 1 || params.Start > len(lines) || params.End < params.Start || params.End > len(lines) {
		return "", fmt.Errorf("invalid line range: %d-%d (file has %d lines)", params.Start, params.End, len(lines))
	}
	newContentLines := strings.Split(params.Content, "\n")
	edited := make([]string, 0, len(lines)-(endIdx-startIdx)+len(newContentLines))
	edited = append(edited, lines[:startIdx]...)
	edited = append(edited, newContentLines...)
	edited = append(edited, lines[endIdx:]...)
	if err := os.WriteFile(cleanPath, []byte(strings.Join(edited, "\n")), 0644); err != nil {
		return "", fmt.Errorf("error writing file %q: %w", cleanPath, err)
	}
	patchStartInEdited := startIdx
	patchEndInEdited := startIdx + len(newContentLines)
	extraContext := 5
	contextStart := max(patchStartInEdited-extraContext, 0)
	contextEnd := min(patchEndInEdited+extraContext, len(edited))
	contextLines := edited[contextStart:contextEnd]
	for i := range contextLines {
		contextLines[i] = fmt.Sprintf("%d\t%s", contextStart+i+1, contextLines[i])
	}
	output, err := json.Marshal(map[string]string{
		"edited_lines": strings.Join(contextLines, "\n"),
	})
	if err != nil {
		return "", fmt.Errorf("error marshalling output: %w", err)
	}
	return string(output), nil
}

func (p patchTool) isPathWithinRoot(targetPath string) (bool, error) {
	rootDir, err := filepath.Abs(".")
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}
	if !strings.HasSuffix(rootDir, string(filepath.Separator)) {
		rootDir += string(filepath.Separator)
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path of target: %w", err)
	}
	if !strings.HasPrefix(targetAbs, rootDir) {
		return false, nil
	}
	cleanPath := filepath.Clean(targetPath)
	if targetPath != cleanPath && strings.Contains(targetPath, "..") {
		return false, errors.New("path contains suspicious directory traversal elements")
	}
	return true, nil
}
