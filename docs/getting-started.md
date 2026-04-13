# Getting Started

## Installation

```sh
go install github.com/alayacore/alayacore@latest
```

## Quick Start

Simply run:

```sh
alayacore
```

On first run, AlayaCore automatically creates a default model config at `~/.alayacore/model.conf` configured for Ollama:

```
---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
---
```

To use other providers, edit the config file — press `Ctrl+L` then `e` in the terminal, or edit it directly. See [configuration.md](configuration.md) for the full format.

Running with skills:

```sh
alayacore --skill ~/playground/alayacore/misc/samples/skills/
```

## CLI Flags

| Flag | Description |
|------|-------------|
| `--model-config string` | Model config file path (default: `~/.alayacore/model.conf`) |
| `--runtime-config string` | Runtime config file path (default: `<model-config-dir>/runtime.conf`, or `~/.alayacore/runtime.conf`) |
| `--system string` | Extra system prompt (can be specified multiple times) |
| `--skill strings` | Skill path (can be specified multiple times) |
| `--session string` | Session file path to load/save conversations |
| `--proxy string` | HTTP proxy URL (e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) |
| `--themes string` | Themes folder path (default: `~/.alayacore/themes`) |
| `--max-steps int` | Maximum agent loop steps (default: 100) |
| `--auto-summarize` | Automatically summarize conversation when context exceeds 80% of limit |
| `--auto-save` | Automatically save session after each response when `--session` is specified (default: enabled) |
| `--plainio` | Use plain stdin/stdout mode instead of terminal UI |
| `--debug-api` | Write raw API requests and responses to log file |
| `--version` | Show version information |
| `--help` | Show help information |

## Examples

```sh
# Basic usage (loads models from ~/.alayacore/model.conf)
alayacore

# With custom model config
alayacore --model-config ./my-model.conf

# With session persistence
alayacore --session ~/my-session.md

# With multiple skill directories
alayacore --skill ./skills1 --skill ./skills2

# With HTTP proxy
alayacore --proxy http://127.0.0.1:7890

# Plain IO mode (stdin/stdout, no terminal UI)
alayacore --plainio

# Piped input
echo "what is 2+2?" | alayacore --plainio

# Show version
alayacore --version
```
