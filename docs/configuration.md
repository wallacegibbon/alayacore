# Configuration

## Model Config File

**Default location**: `~/.alayacore/model.conf`
**Custom location**: Use `--model-config /path/to/model.conf`

The model config file uses a simple key-value format. If the file doesn't exist or is empty, AlayaCore automatically creates it with a default Ollama configuration.

**Important**: After auto-initialization, the program NEVER writes to this file automatically. You must edit it manually with a text editor.

### Format

```
name: "Display Name"
protocol_type: "openai"        # or "anthropic"
base_url: "https://api.example.com/v1"
api_key: "your-api-key"
model_name: "model-identifier"
context_limit: 128000          # optional, 0 = unlimited
prompt_cache: true             # optional, enables cache_control for Anthropic
```

**Fields:**
- `name`: Display name for the model
- `protocol_type`: "openai" or "anthropic"
- `base_url`: API server URL
- `api_key`: Your API key
- `model_name`: Model identifier
- `context_limit`: Maximum context length (optional, 0 means unlimited)
- `prompt_cache`: Enable prompt caching for Anthropic APIs (optional, adds `cache_control` markers)

Separate multiple models with `---`:

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama Local"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
model_name: "llama3"
context_limit: 32768
```

The first model in the file becomes the active model on startup (unless `runtime.conf` has a saved preference).

### Model Selection Logic

1. On startup, AlayaCore reads the model config file
2. If the config file doesn't exist or is empty, it's auto-initialized with a default Ollama configuration
3. The **first model** in the config file becomes the active model (unless `runtime.conf` has a saved preference)

### Editing Models

- Press `Ctrl+L` to open the model selector
- Press `e` to open the config file in your editor ($EDITOR or vi)
- Press `r` to reload models after editing
- Press `enter` to select a model

## Runtime Configuration

**Default location**: `~/.alayacore/runtime.conf`
**Custom location**: Use `--runtime-config /path/to/runtime.conf`

```
active_model: "OpenAI GPT-4o"
active_theme: "theme-dark"
```

The active model is determined by:
1. If `runtime.conf` has a saved `active_model`, that model is used
2. Otherwise, the **first model** in `model.conf` becomes the active model

The active theme is saved to `runtime.conf` when you select a theme in the theme selector (`Ctrl+P`).

## Theme Configuration

Themes are stored as `.conf` files in the themes folder. Each file defines a color scheme:

```
# ~/.alayacore/themes/theme-dark.conf
primary: #89d4fa
dim: #313244
muted: #6c7086
text: #cdd6f4
warning: #f9e2af
error: #f38ba8
success: #a6e3a1
selection: #fab387
cursor: #cdd6f4
added: #a6e3a1
removed: #f38ba8
```

- **Default location**: `~/.alayacore/themes/`
- **Custom location**: Use `--themes /path/to/themes`
- **Auto-initialization**: If the themes folder doesn't exist, AlayaCore creates it with default `theme-dark.conf` and `theme-light.conf`
- **Switching themes**: Press `Ctrl+P` in the terminal to open the theme selector
