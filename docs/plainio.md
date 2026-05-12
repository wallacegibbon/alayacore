# Plain IO Mode

`--plainio` runs AlayaCore as a plain stdin/stdout process with no terminal UI. Useful for scripting, piping, and headless environments.

## Input

- Each line from stdin is a separate prompt
- A trailing backslash (`\`) continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin, waits for queued tasks to finish, exits with code `0`
- **Ctrl-C** (SIGINT): sends `:cancel_all`, exits with code `130` (128+SIGINT)

## Output

All output is plain text with no ANSI escape codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `> prompt` |
| Tool calls | `[tool_name: args]` |
| Tool results | Suppressed |
| Errors | `Error: message` |
| Notifications | `[message]` |

A blank line separates messages of different types.

## Examples

```sh
# Pipe a single question
echo "what is 2+2?" | alayacore --plainio

# Use in scripts
alayacore --plainio < questions.txt > answers.txt
```
