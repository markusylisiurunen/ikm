Writes a file to the local filesystem. Creates parent directories if they don't exist.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Overwrites existing files at the provided path
- If the file exists, reading it first is STRONGLY recommended to understand context and current content
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required
- NEVER proactively create documentation (`.md`, README, etc.) or test files. Only create documentation and test files if explicitly requested by the user
- Only use emojis if the user explicitly requests them. Avoid writing emojis to files unless asked
