# demo-plainio.tape — VHS script for a plain IO mode demo GIF
#
# Usage:
#   vhs demo-plainio.tape
#
# Prerequisites:
#   - VHS installed: go install github.com/charmbracelet/vhs@latest
#   - alayacore on PATH
#   - A working LLM backend configured in ~/.alayacore/model.conf

Output alayacore-demo-plainio.gif

Require alayacore

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 550
Set Margin 8
Set Padding 4
Set BorderRadius 12
Set Theme "Builtin Dark"

# ── Launch in plain IO mode ─────────────────────────────────────────

Hide
Type "cd /home/wallace/playground/alayacore"
Enter
Sleep 500ms
Show

# ── Test 1 ──────────────────────────────────────────────────────────
Type "# Use directly"
Enter
Type "alayacore --plainio"
Enter

Sleep 500ms
Type "what Go version does this project use?"
Enter
Sleep 6s

# ── Exit ────────────────────────────────────────────────────────────

Type ":quit"
Enter
Sleep 1s

# ── Test 2 ──────────────────────────────────────────────────────────

Type "# Use with pipe"
Enter
Sleep 500ms
Type "echo 'what Go version does this project use?' | alayacore --plainio"
Enter
Sleep 6s
