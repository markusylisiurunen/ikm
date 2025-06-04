Reads a file from the local filesystem. You can access any file directly using this tool. Assume this tool can read all files on the machine. If the user provides a file path, assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Supports optional `offset` and `limit` parameters for reading large files in chunks (1-based line numbering)
- When `no_line_numbers` is not set or set to `false`:
  - Returns the file content with line numbers prefixed to each line
  - Line numbers are formatted as `     1	content` (6-digit line number + tab + content)
- When `no_line_numbers` is set to `true`:
  - Returns the raw file content without line numbers
- You can call multiple tools in a single response. It is ALWAYS better to speculatively read multiple files as a batch that are likely useful
- If you are editing a file, it may be useful to read a line range with `no_line_numbers` set to `true` first. This allows you to see the exact content to replace
