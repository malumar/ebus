## 📦 `ebus` – A Generic, Lightweight Event Bus for Go


[![CI](https://github.com/malumar/ebus/actions/workflows/codeql.yml/badge.svg)](https://github.com/malumar/ebus/actions/workflows/codeql.yml)
[![CI](https://github.com/malumar/ebus/actions/workflows/go.yml/badge.svg)](https://github.com/malumar/ebus/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/malumar/ebus.svg)](https://pkg.go.dev/github.com/malumar/ebus)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

`ebus` is a highly extensible, type-safe event bus library for Go with full lifecycle support for events: validation, handling, rollback, commit, and optional transaction dispatch.

Designed to be clean, flexible, and generic using Go's type parameters.

---

### ✨ Features

- ✅ Type-safe payloads using Go generics
- 🔁 Transactional event dispatching with rollback support
- 🧱 Middleware support (logging, tracing, metrics, etc.)
- 🎯 Command pattern with result extraction
- 🔔 Post-commit subscribers per payload type
- 🧪 Fully unit tested

---

### 📦 Installation

Dependencies

- ebus uses only Go’s standard library (fmt, errors, time, encoding/json, etc.). Tests additionally use testify for assertions.


```bash
go get github.com/malumar/ebus
```

---

### Why ebus

ebus was created to leverage Go’s strengths for write-heavy systems without always relying on SQL or pure key–value stores as the central point. In particular:

- Move past SQL bottlenecks for workloads where large datasets fit in RAM and you need very low latency writes at scale.
- Keep the working set in-process for fast startup, fast backups (memory snapshot + WAL), and fast recovery.
- Make it easy to import a production dataset on a developer machine to reproduce and fix issues locally.
- Sustain high write throughput with many concurrent clients.
- Provide a clean, transactional event pipeline that keeps in-memory state (RAM) and persistence (e.g., WAL, files, any store) consistent.

ebus is storage-agnostic: it does not mandate SQL, KV, or a particular database. You can bring your own store and wire it through transactions and staging.

Because ebus ties together the in‑memory and durable transactions under one event flow, replication becomes a first‑class capability rather than an afterthought.

### Production use and performance

ebus is used in production on datasets with tens of millions of records and many long-lived client connections issuing a large number of writes. In these workloads it has proven stable and fast, while maintaining consistency across in-memory state and the persistence layer.

### Architecture at a glance

- Event and payload lifecycle
  - Payloads implement Validate → Handle → (Commit | Rollback).
  - Multiple payloads can be grouped into one Event and processed atomically (all or nothing).

- Two coordinated transactions (replication-friendly)
  - ebus is designed to orchestrate two transactional processes in one logic:
    - TX RAM: your in-memory state and domain effects.
    - TX Persistence: your durable store (e.g., WAL, files, DB).
  - Each can operate independently, but ebus guarantees consistency by:
    - Validating all payloads before any Handle,
    - Staging RAW envelopes (if enabled) before Handle,
    - Rolling back in reverse order on error or panic (+1 includes the failing payload),
    - Committing all payloads only after the outer transaction commits.

- RAW and Staging (optional but recommended for durability)
  - RAW is the original envelope (e.g., JSON) with metadata (EventID, timestamp).
  - If your payload implements RawKeeper, ebus injects the RAW before Handle.
  - If your Tx implements Stager, ebus calls PutRaw before Handle, enabling outbox/WAL-style durability and audit.

### Storage integration

ebus does not impose a storage engine. You can:
- plug in any persistence layer by implementing Transactional and (optionally) Stager,
- stage RAW envelopes (e.g., write-ahead log) before Handle,
- commit the durable side and then finalize in-memory Commit.

In our deployments we use a WAL-based store (project “EDEN”), which complements ebus for persistence and recovery. Any similar WAL/outbox/journal fits the same pattern.

### When ebus is a good fit

- Write-heavy systems where the working set fits in RAM and latency matters.
- You need atomic multi-step operations with precise rollback and panic safety.
- You want to keep the original inbound payloads (RAW) for audit/outbox/WAL.
- You want to replicate or coordinate two transactional layers (RAM + persistence) with a single, consistent event pipeline.


### 🚀 Quick Example

```go
// Simple bus without transactions (add Recovery)
bus := ebus.NewDefault(
	//0 because we are starting, but if you are already running with the database, 
	// you enter the ID of the last event in the database
    ebus.NewID8ByteHandler[AppCtx](0),
    ebus.Recovery[AppCtx, ebus.ID8Byte](log.Printf), // recommended: panic -> error
)
// Single payload
if err := bus.Publish(ctx, &UserData{Login: "john", age: 42}); err != nil {
    // handle error
}

// Many payloads at once, but all or none must be executed - like a transaction
if err := bus.PublishAll(ctx, []ebus.Payload[AppCtx]{
    &UserData{Login: "john", age: 42},
    &UserData{Login: "alice", age: 37},
}); err != nil {
    // handle error
}
```

For more examples look into [ebus_test.go](./ebus_test.go)

#### With result:

```go
// Command (Run – single payload) + RunAll (the first payload is CommandEvent -
// so you expect the result from the function)
res, err := ebus.Run[AppCtx, ebus.ID8Byte, int](ctx, bus, &EventWithResult{I: 42})

 // If you are using multiple payloads in one event, EventWithResult must be the first
res, err := ebus.RunAll[AppCtx, ebus.ID8Byte, int](ctx, bus, []ebus.Payload[AppCtx]{
    &EventWithResult{I: 42},            // must be the first
    &UserData{Login: "john", age: 42},  // Additional payload
})
```

### Dead-letter with RetryWithFallback

The following example shows how to write an event to "dead-letter" after unsuccessful attempts:

```go
type DeadLetterSink interface {
    Put(evt any, err error)
}

sink := newMySink() // e.g. writing to a file, DB, queue, etc.

policy := &myPolicy{max: 3, delay: 200 * time.Millisecond}

bus := ebus.NewDefault(
    idGen,
    ebus.RetryWithFallback[AppState, ebus.ID8Byte](policy, func(evt *ebus.Event[AppState, ebus.ID8Byte], err error) {
        // Save a minimal snapshot, e.g. ID + payload types
        sink.Put(struct{
            ID       ebus.ID8Byte
            Payloads []ebus.PayloadType
            Err      string
        }{
            ID: evt.ID,
            Payloads: func() []ebus.PayloadType {
                out := make([]ebus.PayloadType, len(evt.Payloads))
                for i, p := range evt.Payloads { out[i] = p.PayloadType() }
                return out
            }(),
            Err: err.Error(),
        }, err)
    }),
)
```


---

### 🧹 Payload Lifecycle (Interface & Flow)

Each payload must implement the following interface:

```go
type Payload[T any] interface {
    Validate(ctx T) error   // called before dispatching
    Handle(ctx T) error     // core processing logic
    Commit(ctx T)           // finalize if everything succeeded
    Rollback(ctx T)         // undo if Handle failed
    PayloadType() PayloadType // type identifier used for logging/subscribers
}
```

---

**Lifecycle flow**:

When you call `Publish(...)` or `Run(...)`, each payload goes through the following steps:

1. **Validate** – all payloads must pass before any handling starts
2. **Handle** – called in order; if any fails, rollback begins
3. **Rollback** – executed in reverse order for handled payloads
4. **Commit** – only called if all payloads were successfully handled

This guarantees consistent event processing with rollback support in case of partial failures.

---

### 🧠 Command Support

Commands are payloads that produce results:

```go
type CommandEvent[T, Res any] interface {
    Payload[T]
    Result() Res
}
```

Used with `Run(...)` to publish and return a result atomically.

---

### 🧵 Middleware


Middleware wraps the handler as layered functions: M1 → M2 → Handler → M2 → M1.
Each middleware can run logic before and/or after calling next (e.g., logging,
telemetry, retries, panic recovery).



- **`LoggingByID / LoggingByTypeName`**  
  Logs event and payload information using either numeric or named payload types.

  ```go
  bus := ebus.NewDefault(idGen,
    ebus.LoggingByID(log.Printf),
  )
  ```

  You can customize how payload types are named in logs:

  ```go
    bus := ebus.NewDefault(idGen,
        ebus.LoggingByTypeName(func(pt ebus.PayloadType) string {
            return fmt.Sprint(pt)
        }, log.Printf),
    )
  ```

- **`Retry`**  
  You can automatically retry failed events using `Retry(...)`, This will retry event handling up to 3 times with a 100ms delay between attempts.

  ```go
  bus := ebus.NewDefault(idGen,
    ebus.Retry(3, 100*time.Millisecond),
  )
  ```

- **`RetryWithPolicy`**  
  Retries event dispatch using a custom retry policy. Useful for transient failures.

  Examples in  [ebus_test.go](./ebus_test.go)


- **`RetryWithFallback`**  
  Similar to `RetryWithPolicy`, but also sends failed events to a fallback handler (e.g. dead-letter queue).

  Examples in  [ebus_test.go](./ebus_test.go)


- **`WithEventHooks`**  
  Allows you to execute hooks before and after handling an event (for logging, tracing, etc.).

  Examples in  [ebus_test.go](./ebus_test.go)

---

### 🔔 Subscribers

Note on subscribers
- Subscribe is intended to be called during application startup (single-threaded).
  After startup the subscriber set is treated as immutable; Notifier can run concurrently
  without locks on the hot path.

You can register commit-time hooks:

```go
subs := ebus.NewSubscribers[AppCtx, ebus.ID8Byte]()

subs.Subscribe(&UserData{}, func(ctx AppCtx, p ebus.Payload[AppCtx]) {
    log.Println("User created:", p.(*UserData).Login)
})

bus := ebus.NewDefault(idGen, subs.Notifier())
```

### Non-goals

ebus is not a query engine or ORM. It is a transactional event pipeline and coordinator
for in‑memory state and persistence layers (WAL/outbox/journal/DB).

---

## Advanced Usage

### RAW payloads, RawKeeper, and Staging (what and why)

- RAW is the original, serialized message envelope you received (e.g., JSON from a queue). The bus can attach this envelope to your payload before Handle and optionally “stage” it inside a transaction (e.g., write-ahead log, outbox, audit).
- Use RawKeeper[ID] in your payload to access the incoming RAW. The bus fills Raw.Meta (EventID, TimestampUnix) and sets the Body pointer for you.
- If your transaction type (Tx) implements Stager[ID], the bus calls PutRaw before Handle. This lets you persist the RAW envelope atomically with your domain changes (common in outbox/WAL patterns).
```go
type UserCreate struct {
    ebus.RawPayload[ebus.ID8Byte] // implements RawKeeper
    Login string
    Age   int
}

func (p *UserCreate) Handle(env AppState) error {
    if r := p.Raw(); r != nil {
        env.Logf("RAW type=%v event=%v ts=%d len=%d",
            r.Type, r.Meta.EventID, r.Meta.TimestampUnix, len(r.Body))
    }
    // ... domain logic
    return nil
}
```

To feed RAWs, decode them and publish:

```go
dec := ebus.NewJSONDecoder[AppState]()
dec.MustRegister(UserCreateType, func() ebus.Payload[AppState] { return &UserCreate{} })

raw := []byte(`{"Login":"marcin","Age":82}`)
_ = bus.PublishRaw(env, dec, UserCreateType, raw)
```

With Tx and Stager:

- Start bus with NewWithTx; if your Tx implements Stager[ID], RAWs are staged before Handle.
- On error/panic, TX and payloads are rolled back; staged RAWs are not finalized.

### Recovery (panic safety)

To ensure that a panic during event processing does not crash the process and leaves
no partial state:
- The Tx path (TxDispatcher) has a built‑in recover that converts panic into error,
  rolls back the outer transaction, and rolls back payloads in reverse order (including the failing one).
- For the non‑Tx path (Dispatcher) add the Recovery middleware to convert panic into error.

Example:
```go
bus := ebus.NewDefault(idGen,
ebus.Recovery[AppCtx, ebus.ID8Byte](log.Printf),
)
```

Example (full protection in TxDispatcher – built-in recover):
- embed `defer recover()` in TxDispatcher (see section `Recovery – implementation`).

### Working with context


T (Env/State) does not need to be context.Context. If you need deadlines/cancellation:
- implement Context() context.Context on your Env (ContextCarrier),
- use RetryWithContext (honors cancel/timeout),
- or pass context through your own components.

Example:
```go
type AppState struct { ctx context.Context }
func (s AppState) Context() context.Context { return s.ctx }

bus := ebus.NewWithTx(idGen,
    ebus.RetryWithContext[AppState, ebus.ID8Byte](5, 100*time.Millisecond),
)
```

### NewWithTx requirements (Transactional + Stager)

NewWithTx works with an Env type T that implements:
- Transactional (BeginTx(readonly bool) (Tx, error)),
- optionally Stager[ID] on the Tx (to stage RAW envelopes before Handle).

In short:
- T starts the transaction,
- Tx may stage RAW (files, WAL, DB), detected via type assertion,
- ebus coordinates validation, staging, Handle, rollback, and commit.

### Telemetry and metrics

You can collect metrics through the middleware:
- event handling time, 
- status (success/failure), 
- number of attempts (when using retry),
- number of rollbacks (see below).

Example of simple metrics:
```go
type MetricsSink interface {
    ObserveDuration(eventID any, d time.Duration, success bool)
    ObserveRetry(eventID any, attempt int)
}

func Telemetry[T any, ID any](sink MetricsSink) ebus.EventMiddleware[T, ID] {
    return func(next ebus.EventHandler[T, ID]) ebus.EventHandler[T, ID] {
        return func(env T, evt *ebus.Event[T, ID]) error {
            start := time.Now()
            err := next(env, evt)
            sink.ObserveDuration(evt.ID, time.Since(start), err == nil)
            return err
        }
    }
}
```

You can measure the number of attempts by your own variant `Retry`/`RetryWithPolicy`, which calls `sink.ObserveRetry(evt.ID, attempt)`.

The easiest way to count the number of rollbacks is in the hook where the rollback is called (e.g. by modifying `RollbackRangeUnsafe` or `TxDispatcher` – optional integration).


### Telemetry – ready-made plugins
-  Basic TimeCondition:
```go ebus.go
type MetricsSink interface {
    ObserveDuration(eventID any, d time.Duration, success bool)
    ObserveRetry(eventID any, attempt int)
}

func Telemetry[T any, ID any](sink MetricsSink) EventMiddleware[T, ID] {
    return func(next EventHandler[T, ID]) EventHandler[T, ID] {
        return func(env T, evt *Event[T, ID]) error {
            start := time.Now()
            err := next(env, evt)
            sink.ObserveDuration(evt.ID, time.Since(start), err == nil)
            return err
        }
    }
}
```

- Retries with metrics:
```go ebus.go
func RetryWithMetrics[T, ID any](retries int, delay time.Duration, sink MetricsSink) EventMiddleware[T, ID] {
    return func(next EventHandler[T, ID]) EventHandler[T, ID] {
        return func(env T, evt *Event[T, ID]) error {
            var err error
            for attempt := 0; attempt < retries; attempt++ {
                if sink != nil { sink.ObserveRetry(evt.ID, attempt) }
                if err = next(env, evt); err == nil {
                    return nil
                }
                if attempt < retries-1 {
                    time.Sleep(delay)
                }
            }
            return err
        }
    }
}
```

- Rollback count: if you want the number of rollbacks as a metric, the easiest way is to add a "hook" in RollbackRangeUnsafe (e.g. call an optional global callback or from the Env sink). Example of an idea (requires a slight change in the library code):

```go ebus.go
var onRollbackCount func(n int)

func SetRollbackObserver(cb func(n int)) { onRollbackCount = cb }

func RollbackRangeUnsafe[T, ID any](env T, ev *Event[T, ID], from, to int) {
if onRollbackCount != nil { onRollbackCount(to-from) }
for i := to - 1; i >= from; i-- {
ev.Payloads[i].Rollback(env)
}
}
```

### Concurrency, ordering, and isolation

#### Thread-safety

Bus is safe for concurrent use. You can call Publish/PublishAll from multiple goroutines.
The default ID generator (NewID8ByteHandler) is thread-safe (atomics).
Your components must be safe under concurrency in the way you use them:
 - idGenHandler (if custom),
 - Env/State (especially Transactional.BeginTx),
 - Tx implementations (Commit/Rollback/PutRaw),
 - Decoder/Registry (register types at startup; read-only at runtime),
 - Payload code (Validate/Handle/Commit/Rollback).
Subscribers: Subscribe should be called during startup; after that the subscriber map is treated as immutable, so Notifier can be used concurrently without locks.

#### Ordering guarantees

- Within one event: payloads run in order (Validate all → for each payload: optional PutRaw → Handle; after success Commit all).
- Across events: no global ordering is guaranteed. Concurrent Publish calls may interleave. If you need FIFO or per-key ordering, serialize at the caller or introduce a keyed dispatcher (e.g., a shard-per-key worker pool).

#### Isolation model

- ebus does not impose a transaction isolation level; it relies on your Tx implementation (database/WAL) and any in-memory coordination you add (locks, sharding, single-thread-per-partition).
- Retries (Retry/RetryWithContext/RetryWithPolicy) will re-invoke the handler; re-executions can interleave with other events unless you serialize them.
- Subscribers (post-commit) run after a successful event and are outside of the event transaction boundary; they should not mutate transactional state.
#### Patterns for strict ordering

- Per-key serialization: route events by a partition key to a single worker goroutine (channel) to guarantee per-key FIFO.
- Sharding: N workers, each responsible for a shard (hash(key) % N). Within a shard you get ordering; across shards you get parallelism.

Example outline:
```go

type ShardedDispatcher[T any, ID any] struct {
    shards []chan *ebus.Event[T, ID]
}

func NewShardedDispatcher[T any, ID any](n int, bus *ebus.Bus[T, ID]) *ShardedDispatcher[T, ID] {
    sd := &ShardedDispatcher[T, ID]{shards: make([]chan *ebus.Event[T, ID], n)}
    for i := 0; i < n; i++ {
        sd.shards[i] = make(chan *ebus.Event[T, ID], 1024)
        go func(ch <-chan *ebus.Event[T, ID]) {
            for evt := range ch {
                _ = bus.Apply(/* env */, evt) // or PublishAll via wrapper
            }
        }(sd.shards[i])
    }
    return sd
}

func (sd *ShardedDispatcher[T, ID]) Enqueue(key string, evt *ebus.Event[T, ID]) {
    i := fnv32a(key) % uint32(len(sd.shards))
    sd.shards[i] <- evt
}
```
This pattern guarantees per‑key FIFO within a shard while allowing concurrency across keys.

### Error policy for payload Commit/Rollback
Payload.Commit(env T) and Payload.Rollback(env T) do not return errors by design. This is intentional:

- Commit and Rollback should be best-effort, side-effect–free finalizers (Commit) and compensations (Rollback).
- If something goes wrong inside Commit or Rollback, the payload code should handle it locally (log, metrics, best-effort fixes) and never crash the process.

Recommended reporting patterns

- Log and metrics inside Commit/Rollback:
  - Use your Env to log (e.g., env.Logf) and emit metrics. Consider adding IDs to logs (EventID can be read from RawKeeper if you use RAWs).
- Use hooks for visibility:
  - OnAfterCommit is invoked after a successful event (i.e., after all payload Commit calls): ideal to confirm completion or emit “commit succeeded” metrics.
  - For Rollback visibility, add logging inside your Rollback implementations. If you need a global counter, you can wrap RollbackRangeUnsafe or emit metrics from the TxDispatcher error path (custom middleware).
- Panic safety:
  - If Commit/Rollback panic, TxDispatcher (Tx path) will convert panic to error and roll back TX and payloads. For the non-Tx path (Dispatcher), add the Recovery middleware.

Optional API extension (roadmap)

- If you want to surface Commit/Rollback failures in a structured way, consider extending EventHooks with:
    - OnCommitError(env, event, payload, info string)
    - OnRollbackError(env, event, payload, info string)
- Until then, log/metrics inside payloads and use OnAfterCommit for success signals.


Example (pattern inside a payload)


```go
func (p *UserCreate) Commit(env AppState) {
    if err := p.persistIndex(env); err != nil {
        env.Logf("commit: failed to persist index for %s: %v", p.Login, err)
        // optionally emit metrics; do not return or panic
    }
}
func (p *UserCreate) Rollback(env AppState) {
    if err := p.undoReservation(env); err != nil {
        env.Logf("rollback: failed to undo reservation for %s: %v", p.Login, err)
    }
}
```

### End-to-end TX/WAL example (Stager + RetryWithFallback → dead-letter)
The snippet below shows:

- a Tx implementation that stages RAW to a WAL (PutRaw),
- Commit can fail (simulated) to demonstrate rollback,
- RetryWithFallback routes failed events to a dead-letter sink.

```go
type DeadLetterSink interface {
    Put(evt any, err error)
}

type WALTx struct {
    dir   string
    files []string // staged files
    failCommit bool
}

func (tx *WALTx) PutRaw(r *ebus.Raw[ebus.ID8Byte]) error {
    name := fmt.Sprintf("%v_%x_%d.raw.pending", r.Type, r.Meta.EventID, r.Meta.TimestampUnix)
    path := filepath.Join(tx.dir, name)
    if err := os.MkdirAll(tx.dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
        return err
    }
    if err := os.WriteFile(path, r.Body, 0o644); err != nil {
        return err
    }
    tx.files = append(tx.files, path)
    return nil
}

func (tx *WALTx) Commit() error {
    if tx.failCommit {
        return errors.New("commit failed")
    }
    for _, tmp := range tx.files {
        final := strings.TrimSuffix(tmp, ".pending")
        if err := os.Rename(tmp, final); err != nil {
            return err
        }
    }
    tx.files = nil
    return nil
}

func (tx *WALTx) Rollback() error {
    for _, tmp := range tx.files {
        _ = os.Remove(tmp)
    }
    tx.files = nil
    return nil
}

type EnvWithTx struct {
    walDir string
    // add logging/metrics as needed
}

func (e EnvWithTx) BeginTx(readonly bool) (ebus.Tx, error) {
    return &WALTx{dir: e.walDir}, nil
}

// Build a bus with RetryWithFallback -> dead-letter
func BuildBus(idGen ebus.IDGenHandler[EnvWithTx, ebus.ID8Byte], sink DeadLetterSink) *ebus.Bus[EnvWithTx, ebus.ID8Byte] {
    policy := &ExpoJitterPolicy{Base: 10*time.Millisecond, Factor: 2, Max: 500*time.Millisecond}
    dlq := func(evt *ebus.Event[EnvWithTx, ebus.ID8Byte], err error) {
        // minimal snapshot for the sink
        payloadTypes := make([]ebus.PayloadType, len(evt.Payloads))
        for i, p := range evt.Payloads {
            payloadTypes[i] = p.PayloadType()
        }
        sink.Put(struct {
            ID       ebus.ID8Byte
            Payloads []ebus.PayloadType
            Err      string
        }{ID: evt.ID, Payloads: payloadTypes, Err: err.Error()}, err)
    }
    return ebus.NewWithTx(
        idGen,
        ebus.RetryWithFallback[EnvWithTx, ebus.ID8Byte](policy, dlq),
        ebus.Recovery[EnvWithTx, ebus.ID8Byte](log.Printf), // safety for non-Tx path
    )
}
```

Notes:

- Stager.PutRaw is invoked before Handle for RAW-bearing payloads (RawKeeper).
- If Commit fails, ebus rolls back payloads (reverse order) and calls tx.Rollback.
- RetryWithFallback will retry according to policy and push the final failure to the dead-letter sink.

### 🧪 Testing

Run all tests:

```bash
go test ./...
```
Your suite covers error handling, transactions, and rollback behavior.


### Benchmarks

How to run

Unit tests with race detector:
```bash
go test ./... -race
```

Micro-benchmarks with memory stats:
```bash
go test -bench=. -benchmem ./...
```

Recommended flags for stability:
```bash
go test -bench=. -benchmem -benchtime=2s -count=5 ./...
```

Compare runs with benchstat:
```bash
go install golang.org/x/perf/cmd/benchstat@latest
benchstat old.txt new.txt
```

Bench using makefile:

- micro‑bench (CPU/mem),
```bash
make bench 
```
- more stable results (-benchtime=2s, -count=5),
```bash
make bench-long
```
- race detector,
```bash
make test-race
```
- staticcheck
```bash
make lint
```
---

### ⚖️ License

This project is licensed under the terms of the MIT license. See [LICENSE](./LICENSE).

---

### 👤 About the Author

Created by Marcin Maluszczak (https://registro.pl).  
Feel free to reach out or follow me on [GitHub](https://github.com/malumar).

### 💬 Contributing

Contributions are welcome. Feel free to open issues or pull requests.
