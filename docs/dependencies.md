# Dependencies

This document explains AlayaCore's key dependencies and why each is needed.

## Table of Contents

- [TUI Framework](#tui-framework)
- [Styling](#styling)
- [Low-level Text Processing](#low-level-text-processing)
- [Utility Libraries](#utility-libraries)

---

## TUI Framework

### `charm.land/bubbletea/v2` — Terminal UI Framework

The entire terminal adapter (~50 Go files) is built on top of it. Provides:

- **Event loop**: handles keyboard input, window resize, timers, etc.
- **Component model**: `tea.Model` interface (`Init`/`Update`/`View`) implemented by all UI components
- **Message passing**: `tea.Msg` for communication between components
- **Command system**: `tea.Cmd` for side effects (opening editor, async reads, etc.)
- **Terminal management**: automatic TTY switching, CDC mode, signal forwarding

**Irreplaceable.** Replacing it means rewriting the entire terminal adapter.

---

## Styling

### `charm.land/lipgloss/v2` — Style Rendering

Lip Gloss is Charm's styling library, providing CSS-like style definitions for terminal text. Referenced in 150+ places across the project.

Provides:

- **Style definitions**: foreground, background, bold, italic, underline, etc.
- **Border system**: rounded/thick/hidden borders, used by `RenderBorderedBox` for consistent panel styling
- **Width/height constraints**: `Width()` / `Height()` / `MaxWidth()` for controlling render area
- **Text wrapping**: `WrapWriter` carries ANSI styles across line breaks

**Irreplaceable.** Deeply coupled with Bubble Tea; high replacement cost.

---

## Low-level Text Processing

### `github.com/charmbracelet/x/ansi` — ANSI-aware String Operations

Handles width measurement, truncation, and line-breaking on **text that already contains ANSI escape codes**. Used in three places:

**① Text wrapping (`wrap.go`)**

```go
func wrapContent(s string, width int) string {
    s = ansi.Hardwrap(s, width, false)  // break lines at width
    // ...
}
```

The input to `wrapContent` is Lip Gloss **rendered** output containing `\033[32m...\033[0m` sequences. Line breaking must **ignore ANSI code bytes** and measure only visible characters.

**② Confirmation dialog (`confirm_dialog.go`)**

```go
wrapped := ansi.Hardwrap(styled, innerWidth, false)
line = ansi.Truncate(line, limit, "")
```

Same scenario — text with ANSI styles.

**③ Input field (`input_field.go`)**

```go
valWidth := ansi.StringWidth(visible)  // display width of visible text
```

The text here is **plain text** (user input, no ANSI codes). It reuses the project's existing `ansi.StringWidth` rather than adding a new dependency.

**Why can't `go-runewidth` replace it?**

| Scenario | `ansi` | `runewidth` |
|----------|--------|-------------|
| `Hardwrap("\033[32mHello\033[0m", 3, false)` | `"\033[32mHel\nlo\033[0m"` ✅ | `"\033[32mHel"` ❌ (counts ANSI as visible width) |
| `Truncate("\033[32mHello\033[0m", 3, "")` | `"\033[32mHel\033[0m"` ✅ | `"\033[32mH"` ❌ (truncates mid-ANSI) |
| `StringWidth("\033[32mHello\033[0m")` | `5` ✅ | `16` ❌ (counts ANSI bytes) |

Since the project processes large amounts of Lip Gloss-rendered text (containing ANSI codes), `ansi` is essential.

---

### `github.com/mattn/go-runewidth` — Per-rune Display Width

Provides `RuneWidth(r rune) int`: returns the number of terminal columns a single character occupies.

| Character | `RuneWidth` | Note |
|-----------|-------------|------|
| `'a'` | 1 | ASCII |
| `'中'` | 2 | CJK ideograph |
| `'😀'` | 2 | Emoji |
| `'\t'` | -1 | Tab (special) |

**Why is it needed?** Horizontal scrolling requires knowing each character's width to determine "which character is visible at offset N":

```go
// input_field.go — buildVisibleText
for cells, i := 0, 0; i < len(m.value); i++ {
    w := rw.RuneWidth(m.value[i])  // ← per-rune width needed
    if cells >= m.offset {
        startIdx = i
        break
    }
    cells += w
}
```

**Why can't `ansi.StringWidth` replace it?** `ansi.StringWidth` processes full strings (ANSI parser + grapheme cluster segmentation). Calling it per-rune would be expensive. `runewidth.RuneWidth` is a simple table lookup — zero allocation.

**Why can't `lipgloss.Width` replace it?** `lipgloss.Width(s)` only accepts full strings, not individual runes in a loop.

`runewidth` is already an indirect dependency of `ansi` (`ansi` uses `runewidth` internally for width calculations), so making it a direct dependency doesn't expand the dependency tree.

---

### `github.com/rivo/uniseg` — Unicode Text Segmentation (indirect dependency)

Provides Unicode boundary detection (grapheme clusters, word boundaries, sentence boundaries). It is an **indirect dependency** — used internally by Lip Gloss v2 for correct emoji and combining character width handling.

No direct import needed; it stays in `go.sum` automatically.

```
// Indirect chain:
// lipgloss/v2 → github.com/charmbracelet/x/ansi → github.com/rivo/uniseg
```

---

## Utility Libraries

### `golang.org/x/term` — Terminal Size Detection

```go
// adapter.go
w, h, err := term.GetSize(int(os.Stdout.Fd()))
```

Gets terminal dimensions at startup for initial layout.

### `golang.org/x/sys` — System Calls

Unix signal handling and terminal mode settings. Required by Bubble Tea.

### `golang.org/x/net` — Networking

Used by the project's LLM communication layer.

### `gopkg.in/yaml.v3` — YAML Parsing

Used to load model configuration files and theme definitions.
