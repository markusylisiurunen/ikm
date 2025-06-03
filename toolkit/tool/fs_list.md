Lists git-tracked files in a given directory using `git ls-files`. Returns absolute file paths for up to 10,000 files. Only shows files that are cached, others (untracked), or excluded by .gitignore rules.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Returns empty list if the directory contains no git-tracked files
- Fails if the path is not a directory or doesn't exist
- Limited to 10,000 files maximum for performance
- You should generally prefer the `bash` tool if you know specific files you want to find
