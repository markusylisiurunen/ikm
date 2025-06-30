Call an LLM with a prompt and optional image or PDF files. This tool provides direct access to language models for analysis, generation, and reasoning tasks.

Supported file formats:

- Images: .jpg, .jpeg, .png (automatically resized to maintain quality while meeting API limits)
- Documents: .pdf

Usage notes:

- Choose from available models based on your needs:
  - `claude-sonnet-4`: Highly capable for in-depth analysis, reasoning, and agentic long-horizon tasks. Should be used for most tasks.
  - `gemini-2.5-flash`: Fast and capable, excellent (and preferred) for visual understanding (images) and PDF parsing.
  - `gemini-2.5-pro`: The strongest question-answering model, best for complex reasoning tasks.
- An optional system prompt can be provided to set context for the LLM.
- Files are included after the user prompt in the conversation.

Limitations:

- Claude does not support images or PDFs, so it should only be used for text-based tasks.

When to automatically use this tool (without explicit user request):

- **ONLY** when you need to analyze or extract data from images or PDFs that cannot be processed directly.

When to use this tool after explicit user request to use the `llm` tool:

- When the user specifically asks to use the `llm` tool or a specific model.
- When the user requests analysis that requires advanced reasoning capabilities.
- When the user asks for text, code, or documentation generation with specific requirements and by a specific model.
- When the user requests complex reasoning tasks that would benefit from different model capabilities beyond those of the main agent.

When NOT to use this tool:

- **Never use this tool automatically** unless processing images or PDFs.
- When the main agent can handle the task adequately without delegating to another model.
- For tasks that require real-time interaction or multiple rounds of clarification.
- When the user has not explicitly requested LLM tool usage and no images/PDFs are involved.

Example use cases:

- Analyze a screenshot or diagram and explain what it shows.
- Parse a PDF document and extract key information as JSON.
- Generate code based on detailed specifications by a stronger model.
- Understand complex visual content or charts.
