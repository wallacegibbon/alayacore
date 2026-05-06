# Refactor Tracker

## High Priority

- [ ] 1. Session constructor explosion — extract `SessionConfig` struct (16 params → 3)
- [ ] 2. CommandRegistry is decorative — make it drive dispatch or simplify to metadata
- [ ] 3. Duplicated `toolCallData`/`toolResultData` types — move to `stream` package

## Medium Priority

- [ ] 4. `session.go` god object — extract output helpers, queue logic, compaction
- [ ] 5. Provider code duplication — extract shared SSE/message utilities
- [ ] 6. TOCTOU in `ensureAgentInitialized` — use `sync.Once` or dedicated mutex
- [ ] 7. Unused sentinel errors — wire them up or remove dead definitions

## Low Priority

- [ ] 8. `expandPath` in wrong package — move to `config` or shared utility
- [ ] 9. `GenerateSchema` uses `panic` — return error instead
- [ ] 10. Config parser fragility — add unknown-key validation
- [ ] 11. Oversized `debug/http.go` — audit for dead code
- [ ] 12. Binary artifact + debug logs in repo — add to `.gitignore`
