package agent

import _ "embed"

// summarizePrompt is the prompt sent to the LLM to summarize the
// conversation for continuation. Shared between auto-summarize and
// the :summarize command so behavior stays consistent.
//
//go:embed summarize_prompt.md
var summarizePrompt string
