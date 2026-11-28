# Tool Spinner Refresh During Silent Execution

## Problem

While a tool executes with no output (e.g. `sleep 60`, a long compile, a
slow MCP call), the tool window's header spinner (`TOOL CALL ⠋`) freezes
at the last rendered frame.

Root cause chain:

1. The spinner glyph is **baked into the window's render cache** at render
   time (`BuildFolded`/`BuildInner` → `statusDot()` →
   `spinnerFrame()`, a pure wall-clock function: 10 frames × 150ms).
2. Render caches are only rebuilt when a window is invalidated, and that
   is **delta-driven**: `Af` argument deltas, `Uf` preview deltas, and
   final `UF` frames all call `w.Invalidate()`. A silent tool emits none
   of these.
3. The idle-tick path short-circuits by design (perf commit `7134442b`):
   `handleDisplayRefresh` → `DrainDirty() == false` → no render, and
   `DisplayModel.updateContent` early-exits on `!contentDirty &&
   !IsDirty`. Even the `Program.run` same-content check would skip a
   byte-identical frame.

The session-loading screen does not freeze because `Terminal.View()`
rebuilds `renderLoadingView()` fresh on every tick (a separate, fully
tick-driven path that bypasses the display cache). The tool spinner had
no such driver. The status bar's own indicator needs none either: while a
task runs `renderStatusBar` skips its render cache, so `View()` rebuilds
it — spinner frame and all — on every tick (idle keeps the cache).

## Fix

`WindowBuffer.InvalidateRunningToolSpinners()` (called from
`handleDisplayRefresh` on every tick):

- Scans windows whose renderer is a `*toolRenderer` with status
  `ToolStatusPending` (executing) and calls `w.Invalidate()` +
  `markDirty(i)` — the render cache is rebuilt on the next `GetAll` with
  the current wall-clock frame.
- Returns `false` when no tool is executing, so the idle tick keeps the
  100% skip behavior: benchmarked at **31ns/op, 0 allocs** for a 100-window
  idle buffer (`BenchmarkInvalidateRunningToolSpinnersIdle`, minimum of 10 runs
  at `-benchtime 1s`), and at **33ns/op, 0 allocs** for the same buffer with one
  pending tool (`…Pending`). Both are per tick and neither allocates. The frame
  the invalidation triggers is the ordinary viewport render with one folded
  row rebuilt inside it — what a frame costs, by session shape, is tabulated in
  [virtual-rendering-performance.md](virtual-rendering-performance.md).

### Design notes (read before refactoring)

- **Two independent dirty signals.** `outputWriter.DrainDirty()` consumes
  the output writer's own atomic flag (set by frame handlers); the
  invalidation sets `WindowBuffer.dirty` (consumed by
  `DisplayModel.updateContent` via `IsDirty`). `handleDisplayRefresh`
  therefore merges the two explicitly (`spinnerRefresh || DrainDirty()`).
  The invalidation is done **before** `DrainDirty` so a stream of
  unrelated deltas cannot starve the spinner — the two signals are OR'd
  into one render decision.
- **Status granularity.** Only `ToolStatusPending` is refreshed. Arguments
  streaming (`ToolStatusNone`) is delta-driven by construction — every
  `Af` append invalidates — so polling it would be wasted work.
- **Lock discipline.** `InvalidateRunningToolSpinners` takes `wb.mu`
  alone, never nested with `outputWriter.mu` (see the ordering documented
  in `output.go`). Tool status lives on the renderer inside each window,
  so the model exposes this invalidation rather than handing windows out
  for the presentation layer to mutate under the buffer lock — this
  coupling is deliberate.
- **Why not a session-side heartbeat (periodic `Uf`)?** It would only
  cover the streaming tools (`execute_command`, `search_content`) that own
  a `streamingWriter`; MCP calls and any future non-streaming tool would
  still freeze. It also needs a per-command ticker goroutine and a
  `lastSent`-dedup bypass. The TUI-side fix covers **every** tool with a
  pending window.
- **Why not a render-time glyph patch** (like the cursor row
  replacement in `windowFragment`, or the pinned row's)? The `updateContent`
  early-exit still requires per-tick dirtying, so the hook is not avoided; it
  would only skip one row build (measured in
  [virtual-rendering-performance.md](virtual-rendering-performance.md) — the
  window-row cache). It would extend a documented aliasing pitfall (`lines`
  aliases `w.cache.lines`) into the most fragile, highest-gocyclo rendering
  code for negligible gain.
- **Why `ToolStatusPending` only — not a general animation framework?**
  The spinner is one glyph and one wall-clock function (`spinner.go`), now
  shared by the loading screen, the tool header and the status bar; each
  surface just re-renders from it. A framework would add indirection, not
  clarity.

### Future direction

If the idle scan (31ns/tick) ever matters, replace it with an
`atomic.Int32` running-tool counter maintained at the status transition
sites in `window.go` / `window_buffer.go` — not before profile data
demands it.

## Safety net: step-boundary settlement

`InvalidateRunningToolSpinners` keeps the spinner alive, but nothing in the tick
path can settle a window whose result frame never arrives. That settlement has
two layers, and the first one is the reason a failed step cannot leave animation
running for the rest of the task.

### The step settles the calls it opened

A tool window is opened by the first frame about a call (`AF` start or `Af`
delta) and answered by the frame that closes it (`UF`). Every step therefore owes
one answer per call it announced, and it pays that debt before it returns:
`llm.settleUnansweredToolCalls` runs as `streamEvents` unwinds, and fires a
display-only `UF` error frame (`tool call did not complete`) for every call the
step leaves without a result. The part it returns is discarded, so the call never
enters history — a call that never completed cannot be replayed.

Four shapes arrive there, and they are one rule rather than four fixes:

| Shape | Why no result exists |
|---|---|
| arguments cut off mid-stream | the call never became a part, so nothing can run |
| serial mode, stream died before `finish` | the queue never ran |
| canceled while awaiting confirmation | `runToolCall` runs nothing |
| the tool panicked | the stack is the step's error, not a result |

The rule is stated once, where the calls are known, instead of at each path that
can end a step — which is what makes it hold for a path added later. The frame
lands before the caller hears anything about the failure, so a cut stream's
window shows ✗ before the retry notice that explains the wait.

### The task-completion backstop

A `UF` frame can also be lost entirely — a malformed input frame, a dropped
write, an adapter restart mid-task — and no step-level rule can answer a frame
that never arrived. `WindowBuffer.SettleUnfinishedTools()` covers that: when the
task completion frame arrives (`in_progress` true→false, detected by
`sessionState.updateTask`'s completion edge and handled in
`outputWriter.handleSystemTask`), every tool window still in the running states
(`ToolStatusNone`/`ToolStatusPending`) is settled to `ToolStatusError` (✗) with an
explanation when it has no output.

This is safe by construction: on normal paths every tool settles via its `UF`
frame before the task frame is written (`toolWg.Wait` precedes `handleTaskDone`),
so the settlement only ever touches genuine stragglers. What it cannot do is
anything mid-task, which is why it is the second layer and not the first: a
resumed step leaves the task running, and a spinner the task frame never reaches
turns for the rest of the session.
