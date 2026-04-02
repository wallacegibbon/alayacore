# Terminal Package Refactor — STATE

## Context

Refactoring `internal/adaptors/terminal/`. Package builds with `go build ./...` and tests pass with `go test ./internal/adaptors/terminal/`.

## ✅ Done

### P0 Cleanup (committed `a27f4f9`)
- Unified `restoreFocus` (3 identical methods → 1)
- Unified `confirmKind` enum (3 booleans → enum + 1 bool)
- Removed `GetTotalLinesVirtual` duplicate

### 1. Snapshot-based OutputWriter interface ✅
- Added `StatusSnapshot` / `ModelSnapshot` structs in `interfaces.go`
- Replaced 12 individual getters on `OutputWriter` interface with `SnapshotStatus()` + `SnapshotModels()`
- Implemented both snapshot methods on `outputWriter` (single lock each)
- Deleted 12 old getters: `GetStatus`, `GetQueueCount`, `IsInProgress`, `GetCurrentStep`, `GetMaxSteps`, `GetLastStepInfo`, `GetModels`, `GetActiveModelID`, `GetActiveModelName`, `HasModels`, `GetModelConfigPath` (from outputWriter)
- Updated `updateStatus()` to use `SnapshotStatus()`
- Updated `handleTick()` to use `SnapshotModels()` for model loading
- Updated `adaptor.go` to use `SnapshotModels()` for startup model check
- Updated `keybinds.go` to use `SnapshotModels()` for `openModelConfigFile()`
- Updated tests in `output_laststeps_test.go` to use snapshot methods

### 2. `emitCommand` helper ✅
- Added `func (m *Terminal) emitCommand(cmd string)` helper in `terminal.go`
- Replaced 8 `_ = m.streamInput.EmitTLV(stream.TagTextUser, ...)` + `//nolint` call sites across `terminal.go` and `keybinds.go` with `m.emitCommand(...)`
- Removed `stream` import from `keybinds.go` (no longer needed)

### 3. Decouple Editor from InputModel ✅
- Added `editor *Editor` field to `Terminal` struct
- Moved `OpenEditor()` from `InputModel` to `Terminal` (now accesses `m.editor` directly)
- Updated `handleEditorStart()` to use `m.editor.createTempFile()` instead of `m.input.editor.createTempFile()`
- Updated display key 'e' handler to use `m.editor.OpenForDisplay()` instead of `m.input.editor.OpenForDisplay()`
- Updated `openModelConfigFile()` to use `m.editor.OpenFile()` instead of `m.input.editor.OpenFile()`
- Kept `editorContent` on `InputModel` (it's input state, not editor state)
- Kept `OpenEditor()` in `input_component.go` but receiver changed to `*Terminal`

## 🔧 TODO (ordered by priority)

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
