# Getting Started

## Quick Start

```sh
alayacore
```

On first run, AlayaCore auto-creates a default model config at `~/.alayacore/model.conf` configured for Ollama:

```
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
```

To use other providers, edit the config file — press `Ctrl+L` then `e` in the terminal, or edit it directly. See [configuration.md](configuration.md) for the full format.

## First Steps

1. **Start a conversation** — Type a prompt and press `Enter`. The agent will stream a response.
2. **Give it a task** — Try `"read main.go and explain what this project does"`. The agent will use the `read_file` tool (or `search_content` to find content first), then answer.
3. **Switch models** — Press `Ctrl+L` to open the model selector. Press `e` to edit your config, `Ctrl+R` to reload, `Enter` to select.
4. **Save your session** — Type `:save my-session.alaya` or press `Ctrl+S`.

## Cross-Platform Support

AlayaCore runs on Linux, macOS, and Windows. The `execute_command` tool automatically detects the best available shell on startup:

| OS | Detection order |
|----|----------------|
| **Linux / macOS** | bash → zsh → sh |
| **Windows** | pwsh → powershell → cmd |

The tool description is dynamically adapted so the LLM knows which shell syntax to use. You can override the detection with the `ALAYACORE_SHELL` environment variable:

```sh
# Force PowerShell Core (must be a known shell name)
export ALAYACORE_SHELL=pwsh

# Use zsh
export ALAYACORE_SHELL=zsh
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config-path` | `~/.alayacore/` | Config directory path (contains `model.conf`, `runtime.conf`, `themes/`) |
| `--model` | *(none)* | Model name to activate (must exist in `model.conf`). Highest priority — overrides session file frontmatter and runtime config. |
| `--system` | *(none)* | Extra system prompt text. Repeatable: `--system "rule 1" --system "rule 2"` |
| `--skill` | *(none)* | Path to a skill directory. Repeatable: `--skill ./skills1 --skill ./skills2` |
| `--session` | *(none)* | Path to session file for loading/saving conversations |
| `--proxy` | *(none)* | Proxy URL. Supports `http://`, `https://`, and `socks5://` schemes |
| `--max-steps` | `0` (no limit) | Maximum number of agent loop iterations per prompt. When set to 0 (the default), the agent loops until the model produces a final response. Exceeding this limit raises an error and reports an error — use `:continue` to retry. |
| `--auto-summarize` | `0` (disabled) | Enable auto-summarization at given threshold percentage (e.g. `--auto-summarize=65`, 0 = disabled) |
| `--tool-confirm` | *(none)* | Comma-separated tool `names` that require user confirmation before execution (e.g. `--tool-confirm execute_command,search_content`) |
| `--builtin-tools` | *(all)* | Comma-separated built-in tool `names` to enable. Empty (`--builtin-tools=`) disables all built-in tools. Unspecified means all tools enabled. |
| `--no-delta` | `false` | Disable delta frames (At, Ar, Af); use complete frames only. Reduces wire overhead when the adapter does not need streaming previews. |
| `--rawio` | `false` | Raw TLV stdin/stdout mode — pipe TLV frames directly between agent and controlling process |
| `--plainio` | `false` | Plain stdin/stdout mode — no TUI, for scripting and piping |
| `--debug-api` | `false` | Write raw API requests and responses to a log file |
| `--debug-mcp` | `false` | Write raw MCP JSON-RPC messages to a log file |
| `--version` | — | Print version and exit |
| `--help` | — | Print help and exit |

## Examples

```sh
# Interactive session with default config
alayacore

# Custom config directory (must contain model.conf, runtime.conf, themes/)
alayacore --config-path ./my-config

# Override active model (must match a name in model.conf)
alayacore --model "OpenAI GPT-4o"

# Session persistence
alayacore --session ~/sessions/refactor.alaya

# Multiple skill directories
alayacore --skill ./skills/weather --skill ./skills/pdf

# Behind a proxy
alayacore --proxy http://127.0.0.1:7890

# Plain IO — pipe a question, get an answer
echo "what is 2+2?" | alayacore --plainio
```

## Next Steps

- **[Configuration](configuration.md)** — Set up multiple models, API keys, and themes
- **[Terminal UI](tui.md)** — Learn the keybindings and commands
- **[Plain IO Mode](plainio.md)** — Use AlayaCore without a terminal UI
- **[Raw IO Mode](rawio.md)** — Control AlayaCore programmatically via raw TLV frames
- **[Skills System](skills.md)** — Extend the agent with custom skill packages
