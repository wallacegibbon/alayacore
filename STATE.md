# Terminal Package Refactor — STATE

## Context

Refactoring `internal/adaptors/terminal/`. Package builds with `go build ./...` and tests pass with `go test ./internal/adaptors/terminal/`.

## ✅ Done

### P0 Cleanup (committed `a27f4f9`)
- Unified `restoreFocus` (3 identical methods → 1)
- Unified `confirmKind` enum (3 booleans → enum + 1 bool)
- Removed `GetTotalLinesVirtual` duplicate

### 1. Snapshot-based OutputWriter interface
- Added `StatusSnapshot` / `ModelSnapshot` structs in `interfaces.go`
- Replaced 12 individual getters on `OutputWriter` interface with `SnapshotStatus()` + `SnapshotModels()`
- Implemented both snapshot methods on `outputWriter` (single lock each)
- Deleted 12 old getters from `outputWriter`
- Updated all callers: `updateStatus()`, `handleTick()`, `adaptor.go`, `keybinds.go`, tests

### 2. `emitCommand` helper
- Added `func (m *Terminal) emitCommand(cmd string)` helper
- Replaced 8 `_ = m.streamInput.EmitTLV(...)` + `//nolint` call sites with `m.emitCommand(...)`
- Removed `stream` import from `keybinds.go`

### 3. Decouple Editor from InputModel
- Added `editor *Editor` field to `Terminal` struct
- Moved `OpenEditor()` from `InputModel` to `Terminal` receiver
- Updated `handleEditorStart()`, display 'e' key, `openModelConfigFile()` to use `m.editor`

### 4. Map-based display key dispatch
- Replaced `handleDisplayKeys()` switch with `displayKeyMap` map of `func(*Terminal) tea.Cmd`
- Eliminated `nolint:gocyclo` annotation

### 5. Remove ModelConfig duplication
- Replaced `ModelConfig` with `searchableModel` embedding `agentpkg.ModelInfo`
- Deleted orphaned `OpenModelConfigFile()` function
- Removed unused `os`, `os/exec` imports

### 6. Remove global WarningCollector
- Replaced `var globalWarningCollector` with explicit DI via `*WarningCollector`
- `WarningCollector` now owned by `ThemeManager`
- `AddWarningf()` is nil-safe helper taking `*WarningCollector` param
- `Terminal.Init()` drains warnings via `ThemeManager.GetWarnings()`

### 7. Consistency fixes
- Standardized `DisplayModel` receivers to pointer (`*DisplayModel`)

## 🔧 TODO

(none remaining — all planned refactors complete)
