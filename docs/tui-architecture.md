# TUI Architecture: Elm, Bubble Tea, and AlayaCore's Design

## The Elm Architecture (Reference Model)

The Elm architecture is built on three core concepts:

```
Model  →  application state (single value)
Update →  pure function: (Model, Msg) → (Model, Cmd)
View   →  pure function: Model → Html (rendering)
```

Properties:
- **Immutable state** — `Update` returns a new `Model`, never mutates the old one
- **Side effects as data** — `Cmd` describes what to do, not how to do it
- **Composition** — child components nest inside parent models

## Bubble Tea: Elm adapted for Go

Bubble Tea's `tea.Model` interface:

```go
type Model interface {
    Init() tea.Cmd
    Update(msg tea.Msg) (tea.Model, tea.Cmd)
    View() tea.View
}
```

### What Bubble Tea keeps from Elm

| Concept | Elm | Bubble Tea |
|---------|-----|------------|
| Model-Update-View | ✅ | ✅ |
| Messages drive changes | ✅ | ✅ (tea.Msg) |
| Side effects via Cmd | ✅ | ✅ (tea.Cmd) |

### What Bubble Tea changes

| Aspect | Elm | Bubble Tea | Reason |
|--------|-----|------------|--------|
| Model Update | Pure function, no mutation | Pointer receiver mutates + returns self | Go performance & idiom |
| Messages | Algebraic data types (exhaustive) | `interface{}` + runtime type switch | Go has no sum types |
| Cmd | Data (inspectable record) | Opaque `func() Msg` | Simpler runtime |
| Sub-components | All implement Model | Plain structs with methods OK | Optional composition |

## AlayaCore TUI Design

### Before Refactoring (Problems)

- All components stored as **pointers** (`*PromptInput`, `*ModelSelector`, etc.)
- **Mutation shortcuts** like `updateFromMsg` bypassed the Update chain
- **Consume* polling**: after `HandleKeyMsg`, callers polled `ConsumeModelSelected()`, `ConsumeThemeSelected()`, etc.
- **Dead Update methods** on non-root components that implemented `tea.Model` but were never called
- **InputField used closures** (`cursorRender`, `promptRender`) making it non-copyable

### After Refactoring

#### Principle: Every component is a value type

```
Terminal          value receiver Update → (Terminal, tea.Cmd)
├── input         PromptInput    value type, value receiver methods
│   └── input     InputField     value type, value receiver Update
├── display       DisplayModel   value type, value receiver methods
│   └── scrollView ScrollView    value type, value receiver methods
└── overlays      OverlayManager (stores value types, has setters)
    ├── modelSelector    ModelSelector     HandleKeyMsg → (Self, Result)
    ├── themeSelector    ThemeSelector     HandleKeyMsg → (Self, Result)
    ├── helpWindow       HelpWindow        HandleKeyMsg → (Self, Result)
    ├── confirmOverlay   ConfirmDialog     HandleKeyMsg → (Self, Result)
    ├── mcpInitOverlay   ConfirmDialog     HandleKeyMsg → (Self, Result)
    └── attachmentWindow AttachmentWindow  HandleKeyMsg → (Self, Result)
```

#### Rules

1. **All components are value types** — no shared mutable pointers.
   Exception: shared read-only state (`*Styles`, `*WindowBuffer`, `*[\]attachment`).
2. **All mutation methods are value receivers returning `Self`** — no in-place mutation.
3. **All callers capture return values** — `m.input = m.input.SetValue("")`, never `m.input.SetValue("")`.
4. **Results are explicit** — `HandleKeyMsg` returns a typed result struct, no `Consume*` polling.
5. **No dead code** — no unused `tea.Model` implementations.
6. **Root model uses pointer receivers internally** — `Update` is value receiver, but internal helpers (`handleKeyMsg`, `handleTick`, etc.) use `*Terminal` for convenience.

#### Data Flow

```
tea.KeyMsg
  │
  ▼
Terminal.Update(msg)          value receiver (m is a copy)
  │
  ├─ m.handleKeyMsg(msg)      Go auto-addrs &m, pointer to copy
  │   ├─ handleSelectorOverlayKeys → overlay.HandleKeyMsg(msg)
  │   │                                    │
  │   │                              returns (Self, Result)
  │   │                                    │
  │   │                              caller: overlays.SetModelSelector(ms)
  │   │
  │   └─ handleFallback → m.input.Update(msg)
  │
  └─ return m, cmd             return updated copy
```

### Key Changes by Phase

| Phase | Change | Rationale |
|-------|--------|-----------|
| 1 | Remove `updateFromMsg` shortcut | All state changes must go through `Update` |
| 2 | `InputField` value type (remove closures) | Make it copyable with value semantics |
| 3 | All overlays → value types | Consistency, no hidden pointer mutations |
| 4 | `Consume*` → explicit result types | Implicit polling protocol replaced by typed return values |
| 5 | Terminal `Init`/`Update`/`View` → value receivers | Root model satisfies `tea.Model` consistently |

### Remaining Differences from Elm

| Aspect | Pure Elm | Our Code | Acceptable? |
|--------|----------|----------|-------------|
| Messages | Sum types with exhaustive matching | `interface{}` + type switch | Yes (Go limit) |
| Cmd | Data | Function | Yes (BT convention) |
| Internal helpers | Pure functions | Pointer receivers | Yes (root model only) |
| Shared state | None | `*Styles`, `*WindowBuffer` | Yes (read-only after init) |
