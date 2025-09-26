## [v1.3.0] - 2025-09-26


### Breaking changes
- Terminology/API: replaced `Ctx` with `Env`/`State` in interfaces and examples. It better reflects “application state” or “transaction”.
- Generic RAW: `RawKeeper` and `RawPayload` are now generic over `ID` (`RawKeeper[ID]`, `RawPayload[ID]`). Event metadata (EventID, timestamp) is now strongly typed.
- Transactions: `NewWithTx` now relies on `Transactional` (requires `BeginTx`). Optional RAW staging is detected via type assertion `Stager[ID]` on `Tx`.
- Staging: RAW staging (PutRaw) is performed right before `Handle` for each payload (inside `ApplyAllUnsafe`). On error, rollback also includes the failing payload (+1).
- `TxDispatcher`: internal handler now returns a named error and has built‑in `recover` (panic -> error + rollback TX + rollback payloads).
- Error handling: switched to Go stdlib `errors` + `fmt.Errorf("%w")` (no more `pkg/errors`).
- JSONDecoder: `DisallowUnknownFields` is enabled (unknown JSON fields cause an error).
- Validation: `ValidateAll` still takes `Event[T, ID]`, but a simpler `ValidatePayloads[T]` (without ID) is recommended if you add it (backward compatibility kept).
- Renamed LoggingById to LoggingByID (Go naming idiom).

### Added
- Built‑in panic protection in `TxDispatcher` (rolls back TX and payloads, returns error to the caller).
- `Recovery` middleware – turns panics into errors (recommended for `NewDefault`/Dispatcher).
- `RetryWithContext` middleware – honors `Env.Context()` (deadline/cancel).
- Telemetry examples: `Telemetry` / `RetryWithMetrics` (duration, attempts).
- Type registry: records first registration origin (file:line) when built with `-tags ebusdebug` (zero overhead in production).

### Changed
- Rollback semantics: on `Handle` error or panic, rollback runs in reverse order and includes the failing payload (nHandled+1).
- Better staging diagnostics: PutRaw errors are returned as event errors with clear message (`stage raw (type=%v): ...`).

### Migration notes
- Update payloads to `RawPayload[YourID]` and `RawKeeper[YourID]` if you use RAW.
- If you used `NewWithTx`, your `Env` must implement `Transactional`. Optional staging is detected via `Stager[ID]` on `Tx`.
- Add `Recovery` to `NewDefault(...)` (Dispatcher does not have built‑in recover).
- If you decode JSON RAW: make sure fields are exported (e.g., `Age` not `age`), otherwise validation/Handle may not work as expected.



## [v1.2.0] - 2025-04-01

### Update

- `NewID8ByteHandler` allows you to set the starting ID value

## [v1.1.0] - 2025-04-01

### ✨ Added
- `LoggingById / LoggingByTypeName` middleware for logging
- `RetryWithPolicy` middleware for customizable retry logic.
- `RetryWithFallback` middleware with fallback handler (e.g. dead-letter queue).
- `WithEventHooks` middleware to inject pre- and post-processing hooks.

### 📝 Documentation
- Expanded README with a dedicated Middleware section and examples.

### ✅ Tests
- Added test coverage for new middleware features including retry and event hooks.
