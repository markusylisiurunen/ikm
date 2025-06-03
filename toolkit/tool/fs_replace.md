Performs exact string replacements in files up to 10MB in size.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Reading the file first is STRONGLY recommended for understanding context and the file's up-to-date content
- When editing text from `fs_read` tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: spaces + line number + tab. Everything after that tab is the actual file content to match. Never include any part of the line number prefix in the `old_string` or `new_string`
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required
- NEVER call this tool in parallel. If you need to make multiple edits, do them sequentially in separate calls
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked
- The edit will FAIL if `old_string` is not unique in the file (unless `replace_all` is true). Either provide a larger string with more surrounding context to make it unique or use `replace_all` to change every instance of `old_string`
- Use `replace_all` for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance
