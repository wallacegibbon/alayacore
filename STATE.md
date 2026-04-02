# Terminal Package Refactor — STATE

## Context

Refactoring `internal/adaptors/terminal/`. Package builds with `go build ./...` and tests pass with `go test ./internal/adaptors/terminal/`.

## ✅ Done

### P0 Cleanup (committed `a27f4f9`)
- Unified `restoreFocus` (3 identical methods → 1)
- Unified `confirmKind` enum (3 booleans → enum + 1 bool)
- Removed `GetTotalLinesVirtual` duplicate

## 🔧 TODO (ordered by priority)

### 1. Snapshot-based OutputWriter interface
- **Goal**: 12 individual getters → 2 snapshot structs (`SnapshotStatus()`, `SnapshotModels()`)
- **Files**: `interfaces.go`, `output.go`, `terminal.go`, `model_selector.go`
- **Steps**: Add `StatusSnapshot`/`ModelSnapshot` types → replace interface methods → implement on `outputWriter` (one lock each) → delete 12 getters → update `updateStatus()` to use snapshot → update `handleTick()` model loading → update `adaptor.go` model checks

### 2. `emitCommand` helper
- **Goal**: Replace 11 `_ = m.streamInput.EmitTLV(...)` + `//nolint` with one helper that logs errors
- **Files**: `terminal.go`, `keybinds.go`
- **Steps**: Add `func (m *Terminal) emitCommand(cmd string)` → replace all 11 call sites

### 3. Decouple Editor from InputModel
- **Goal**: Move `Editor` from `InputModel.editor` to `Terminal.editor`
- **Files**: `terminal.go`, `input_component.go`, `keybinds.go`
- **Steps**: Add `editor *Editor` to `Terminal` → remove from `InputModel` → move `OpenEditor()` to `Terminal` → update all `m.input.editor.X()` → `m.editor.X()`

### 4. Map-based display key dispatch
- **Goal**: Replace `nolint:gocyclo` switch with `map[string]func` table
- **File**: `keybinds.go`
- **Steps**: Define `displayKeyMap` map → extract each case into named method → replace switch with map lookup

### 5. Remove ModelConfig duplication
- **Goal**: `ModelConfig` duplicates `agentpkg.ModelInfo` → use `searchableModel` wrapper
- **File**: `model_selector.go`

### 6. Remove global WarningCollector
- **Goal**: Package-level `var globalWarningCollector` → explicit DI
- **Files**: `warnings.go`, `theme_manager.go`, `styles.go`, `adaptor.go`

### 7. Consistency fixes
- Standardize `DisplayModel` receivers (value → pointer)
- Delete orphaned `OpenModelConfigFile` in `model_selector.go`
- Fix `View()` return types on internal components
