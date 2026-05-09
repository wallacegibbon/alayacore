# Tool Output Handling

How AlayaCore handles large outputs from tools to stay within context budgets.

## Strategy

| Tool | Behavior | File Pattern |
|------|----------|--------------|
| `read_file` | Truncates at 64KB with metadata header | N/A (in-memory) |
| `execute_command` | Saves to file | `.alayacore.tmp/cmd-*.txt` |
| `search_content` | Saves to file | `.alayacore.tmp/search-*.txt` |

## read_file

Files larger than 64KB are truncated at a line boundary with metadata:

```
[Lines 1-3375 of 10000 | 64.0KB of 760.6KB shown]

[file content...]
```

- Agent can use `start_line`/`end_line` to read specific ranges
- No file is created; truncation happens in-memory

## execute_command

Command output larger than 64KB is saved to a temp file:

```
Output (5000 lines, 194.2KB) saved to: .alayacore.tmp/cmd-12345.txt
Use read_file to access specific sections.
```

Or with error:
```
Exit Code: 1
Output (5000 lines, 194.2KB) saved to: .alayacore.tmp/cmd-12345.txt
Use read_file to access specific sections.
```

- Agent uses `read_file` with line ranges to access specific sections
- Same behavior for canceled/timed out commands

## search_content

Search results exceeding `max_lines` (default 100) are saved to a temp file:

```
Search found 500 matching lines. Results saved to: .alayacore.tmp/search-12345.txt
Use read_file to access specific matches.
```

- Agent uses `read_file` to access the full results from the saved file

## Temp File Location

All temp files are saved to `.alayacore.tmp/` in the current working directory.

**Why CWD instead of /tmp?**
- Avoids cross-filesystem issues when `/tmp` is on a different mount
- Same approach as `edit_file` for atomic file operations
- Uses `os.CreateTemp` for atomic file creation

**Cleanup:**
```bash
rm -rf .alayacore.tmp/
```

Or add to `.gitignore`:
```
.alayacore.tmp/
```

## Related

- [Context Tracking](context-tracking.md) — How context tokens are tracked across API calls
- [Error Handling](error-handling.md) — `max_tokens` truncation vs. errors
