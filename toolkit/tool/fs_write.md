Writes a file to the local filesystem. Creates parent directories if they don't exist.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- This tool will overwrite the existing file if there is one at the provided path
- Content is limited to 10MB maximum
- If exists, reading the file first is STRONGLY recommended for understanding context and the file's up-to-date content
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required
- NEVER proactively create documentation files (`.md`) or README files. Only create documentation files if explicitly requested by the user
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked
