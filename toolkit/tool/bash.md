Executes a given bash command in a persistent shell session, ensuring proper handling and security measures.

Usage notes:

- The command argument is required.
- **VERY IMPORTANT:**
  - The sandbox (where commands run) has a read-only filesystem (files cannot be modified) and network access is disabled. You MUST NEVER attempt to modify files or access the network.
    - You can run any command that only reads from the filesystem and does not require network access.
  - You MUST prefer searching files using `ripgrep` (`rg`) over `grep`/`find`/etc. for performance and efficiency, which all users have pre-installed.
    - Find a pattern in a specific file: `rg '<pattern>' <file>`
    - Find a pattern with context (3 lines before/after): `rg -C 3 '<pattern>' <file>`
    - Find a pattern in all Go files within a directory: `rg -g "*.go" '<pattern>' <directory>`
    - List files containing a pattern in a directory: `rg -l '<pattern>' <directory>`
  - You MUST avoid read tools like `cat`, `head`, `tail`, and `ls`, and instead use `fs_read` and `fs_list` to read files.
- Git is fully working in the sandbox. You may use read-only git commands for common operations like viewing current changes (`git status`, `git diff`), comparing commits (`git diff <commit1> <commit2>`), viewing commit history (`git log`), and other inspection operations.
- When issuing multiple commands, use the `;` or `&&` operator to separate them. DO NOT use newlines (newlines are acceptable in quoted strings).
- You have the capability to call multiple tools in a single response. When multiple independent pieces of information are requested, batch your tool calls together for optimal performance.
- Try to maintain your current working directory throughout the session by using absolute paths and avoiding use of `cd`. You may use `cd` if the user explicitly requests it.
  <good-example>
  pytest /foo/bar/tests
  </good-example>
  <bad-example>
  cd /foo/bar && pytest tests
  </bad-example>
