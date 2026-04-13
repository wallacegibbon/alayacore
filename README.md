# AlayaCore

A minimal AI agent with tool-calling, powered by Large Language Models. It provides tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible and Anthropic-compatible API servers.

## Quick Start

```sh
go install github.com/alayacore/alayacore@latest
alayacore
```

On first run, AlayaCore auto-creates a default model config at `~/.alayacore/model.conf` configured for Ollama.

## Features

- **Tools**: read_file, edit_file, write_file, activate_skill, shell
- **Multi-provider**: OpenAI, Anthropic, DeepSeek, Qwen, Ollama, LM Studio
- **Interactive TUI**: Bubble Tea terminal UI with vim-like keybindings
- **Streaming**: Real-time streaming output with color styling
- **Skills system**: [agentskills.io](https://agentskills.io) compatible
- **Session persistence**: Save/load conversations, auto-save support
- **Plain IO mode**: stdin/stdout for scripting and piping (`--plainio`)
- **Proxy support**: HTTP/HTTPS/SOCKS5
- **Theming**: Customizable color themes with live switching
- **Multi-step agent loop**: Automatic tool-calling with configurable max steps

## Documentation

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | Installation, quick start, CLI flags, and examples |
| [Configuration](docs/configuration.md) | Model config, runtime config, and themes |
| [Terminal UI](docs/terminal-ui.md) | Keybindings, commands, window container, task queue, plain IO mode |
| [Architecture](docs/architecture.md) | Layered architecture, TLV protocol, data flow, file organization |
| [Skills System](docs/skills.md) | Agent Skills specification, usage, and SKILL.md format |

### Design Docs

| Document | Description |
|----------|-------------|
| [Sequential Tool Execution](docs/sequential-tool-execution.md) | Why tools execute one at a time |
| [Error Handling](docs/error-handling.md) | How LLM API errors are detected and propagated |
| [Context Token Tracking](docs/context-tracking.md) | How context size is tracked across providers |
| [Virtual Rendering Performance](docs/virtual-rendering-performance.md) | Performance analysis of the virtual scrolling system |

## License

MIT
