Launch autonomous AI agents that have access to the following tools: bash, file system (read, write, list, edit), LLM calls, and reasoning. Use this tool for complex multi-step tasks that require autonomous execution, file manipulation, or parallel processing across multiple targets.

When to use the `task` tool:

- For complex multi-step workflows that require autonomous execution (e.g., "analyze all Python files and generate a summary report")
- When you need to process multiple files or targets in parallel (e.g., "run the same analysis on files A, B, and C")
- For tasks requiring bash command execution combined with file operations
- When you need an agent to work autonomously without further input from you
- For research tasks that involve reading multiple files and synthesizing information without a clear idea of the steps required to reach the goal

When NOT to use the `task` tool:

- For simple single-file operations – use direct file tools instead for faster execution
- When you need real-time interaction or clarification during execution
- For tasks that require user input or confirmation during execution
- For simple bash commands that don't require file analysis

Effort levels:

- Fast: Uses a capable model optimized for speed. Best for: file operations, simple/intermediate code changes, data extraction, basic analysis
- Thorough: Uses the most advanced model available. Best for: complex reasoning, architectural decisions, multi-step workflows, research tasks

Usage notes:

- Launch multiple agents concurrently whenever possible to maximize performance
- Each agent execution is stateless and autonomous – they cannot request clarification
- Agents have a 5-minute execution timeout
- Use variable substitution ({{file_path}}, {{command}}, etc.) to customize prompts per agent
- Your task prompt should contain detailed instructions since agents operate autonomously
- Clearly specify what information the agent should return in its final report
- Tell the agent whether you expect it to write code, perform analysis, or conduct research

Example task prompt:

<example_prompt>
Find instances of {{symbol}} in all Python files, starting with {{file_path}}. Report back with a clear list of all occurrences, including the file names and line numbers.
</example_prompt>
<example_variables>
{"symbol": "my_function", "file_path": "/path/to/start/file"}
</example_variables>
