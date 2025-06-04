Performs exact string replacements in files.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Reading the file first is STRONGLY recommended to understand context and current content
- When editing text from `fs_read` tool output with line numbers, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: spaces + line number + tab. Everything after that tab is the actual file content to match. Never include any part of the line number prefix in the `old_string` or `new_string`
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required
- NEVER call this tool in parallel. If you need to make multiple edits, do them sequentially in separate calls
- The edit will FAIL if `old_string` is not unique in the file (unless `replace_all` is `true`). Either provide a larger string with more surrounding context to make it unique, or use `replace_all` to change every instance of `old_string`
- Use `replace_all` for replacing and renaming strings across the file. This parameter is useful when you want to rename a variable, for instance
- If you struggle with editing a file by replacing a string, consider using `fs_write` instead to rewrite the entire file with the new content
- Only use emojis if the user explicitly requests them. Avoid adding emojis to files unless asked
