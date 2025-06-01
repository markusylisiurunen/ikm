> Essential high-level context for the `ikm` agentic coding assistant.

## Project overview

IKM is a terminal-based AI coding assistant featuring:

- Natural language interaction with local codebases.
- Sandboxed code execution via Docker.
- Multi-modal AI integration (Claude, GPT, Gemini via OpenRouter).
- Extensible tool system for file operations and code analysis.
- Real-time streaming responses with cost tracking.

## How to work

- Do no build the code (`go build`) or run tests (`go test`). This is handled for you after you make changes.
- Never write tests for your code.
- Code style preferences:
  - Always match the code style of the surrounding code, including comment style and whitespace.
  - Use lowercase comments unless uppercase is justified (e.g., proper nouns, acronyms).

## High-level architecture

Key directories and their primary roles:

```text
cmd/ikm/              # Entry point, Docker integration, system prompts
internal/agent/       # Core agent: conversation, state, event management
internal/logger/      # Structured JSON logging
internal/tui/         # Bubble Tea terminal interface
toolkit/llm/          # LLM abstraction, OpenRouter integration, tool definition
toolkit/tool/         # Built-in tool implementations
```

## Core systems overview

### Agent system (`internal/agent/`)

- **Purpose**: Manages conversation flow, tool execution, and application state.
- **Architecture**: Event-driven. Communicates updates (content, tool calls/results, errors, usage) to subscribers (e.g., TUI) via channels.
- **Key responsibilities**:
  - Maintaining message history.
  - Orchestrating LLM interactions.
  - Dispatching tool calls and processing their results.
  - Ensuring thread-safe operations on shared state.

### TUI system (`internal/tui/`)

- **Purpose**: Provides the terminal user interface using Charm's Bubble Tea framework.
- **Components**: Main elements include a viewport for displaying the conversation, a text input area for user messages, and a footer for metadata (like cost and current model).
- **Key responsibilities**:
  - Handling user input, including slash commands (e.g., `/clear`, `/mode`, `/model`) for controlling the application.
  - Rendering the conversation, including user messages, assistant responses, tool usage indicators, and errors.
  - Updating its state based on events received from the Agent system.

### LLM toolkit (`toolkit/llm/`)

- **Purpose**: Abstracts interactions with various AI models, primarily through the OpenRouter service.
- **Key interface**: `llm.Model` defines the contract for streaming responses and registering tools.
- **Key responsibilities**:
  - Managing communication with LLM APIs, including streaming content.
  - Supporting multi-modal inputs (text, images, files where applicable).
  - Facilitating the tool-calling mechanism by sending tool specifications to the LLM and parsing tool use requests.
  - Structuring messages (`llm.Message`) for conversation history.
  - Tracking token usage and estimated costs for LLM interactions.

### Tool system (`toolkit/tool/`)

- **Purpose**: Extends the agent's capabilities by allowing it to perform actions and gather information.
- **Interface**: Tools implement the `llm.Tool` interface, which requires:
  - `Spec()`: Defines the tool's name, description, and a JSON schema for its arguments. This is provided to the LLM.
  - `Call()`: Executes the tool's logic with the arguments provided by the LLM.
- **Built-in tools**: Cover functionalities such as:
  - `bash`: Sandboxed command execution within a Docker container.
  - `fs`: File system operations (listing, reading, writing, removing files).
  - `llm`: Making nested or specialized calls to other LLM models.
  - `task`: Autonomous execution of complex, multi-step tasks by a sub-agent with its own toolset.
  - `think`: A simple tool for the agent to log its internal reasoning steps.
- **Registration**: Tools are registered with the current `llm.Model` instance to make them available for the LLM to use.

### Logger system (`internal/logger/`)

- **Purpose**: Provides structured JSON logging capabilities.
- **Output**: Logs are typically written to files in the `.ikm/logs/` directory.
- **Configuration**: Logging verbosity (e.g., debug level) is usually controlled by an environment variable like `DEBUG=true`.

## Key development patterns & extension points

- **Event-driven architecture**: The Agent system is central, using events (Go channels) to communicate state changes and updates to other parts of the application, particularly the TUI. This decouples components and facilitates real-time updates.
- **Adding a new tool**:
  1. Create a new Go file for your tool, typically in `toolkit/tool/`.
  2. Check an existing tool implementation for reference to match the structure and coding patterns.
  3. Implement the `llm.Tool` interface, ensuring `Spec()` provides an accurate JSON schema for the LLM and `Call()` contains the tool's logic.
  4. Instantiate and register your new tool with the `llm.Model` where it's initialized or updated (e.g., in `internal/tui/tui.go` during setup or model switching).
- **Adding a new LLM model**:
  1. Modify `toolkit/llm/openrouter.go` to include the new model's definition, slug, and any specific handling it might require.
  2. Update TUI components if necessary to allow users to select the new model.
- **Adding a new mode (system prompt)**:
  1. Create a new `.txt` file containing the system prompt in `cmd/ikm/prompts/`.
  2. Embed this prompt file in `cmd/ikm/main.go`.
  3. Update `internal/tui/tui.go` to handle the new mode, including its selection via slash commands and setting it on the agent.

## Debugging

- **Focus areas when debugging**:
  - The sequence of events emitted by the Agent: Look for `ContentDeltaEvent`, `ToolUseEvent`, `ToolResultEvent`, and `ErrorEvent` to trace the conversation flow.
  - Tool interactions: Verify the arguments in `ToolUseEvent` and the output in `ToolResultEvent`.
  - LLM API communication: Check request/response details with OpenRouter.
  - TUI state: Ensure the TUI correctly reflects the agent's state and events.
