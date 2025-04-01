## 📦 `ebus` – A Generic, Lightweight Event Bus for Go

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

```bash
go get github.com/malumar/ebus
```

---

### 🚀 Quick Example

```go
bus := ebus.NewDefault(ebus.NewID8ByteHandler[AppCtx]())

err := bus.Publish(ctx, []ebus.Payload[AppCtx]{
    &UserData{Login: "john", Age: 42},
})
```

For more examples look into [ebus_test.go](./ebus_test.go)

#### With result:

```go
res, err := ebus.Run[AppCtx, ebus.ID8Byte, int](ctx, bus, []ebus.Payload[AppCtx]{
    &EventWithResult{I: 42},
})
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

Middleware is applied per event dispatch:

- **`LoggingById / LoggingByTypeName`**  
  Logs event and payload information using either numeric or named payload types.

  ```go
  bus := ebus.NewDefault(idGen,
    ebus.LoggingById(log.Printf),
  )
  ```

  You can customize how payload types are named in logs:

  ```go
  bus := ebus.NewDefault(idGen,
    ebus.LoggingByTypeName(func(pt ebus.PayloadType) string {
        return pt.String() // or custom mapping like a map[PayloadType]string
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

You can register commit-time hooks:

```go
subs := ebus.NewSubscribers[AppCtx, ebus.ID8Byte]()

subs.Subscribe(&UserData{}, func(ctx AppCtx, p ebus.Payload[AppCtx]) {
    log.Println("User created:", p.(*UserData).Login)
})

bus := ebus.NewDefault(idGen, subs.Notifier())
```

---

### 🧪 Testing

Run all tests:

```bash
go test ./...
```

Your suite covers error handling, transactions, and rollback behavior.

---

### ⚖️ License

This project is licensed under the terms of the MIT license. See [LICENSE](./LICENSE).

---

### 👤 About the Author

Created by Marcin Maluszczak (https://registro.pl).  
Feel free to reach out or follow me on [GitHub](https://github.com/malumar).

### 💬 Contributing

Contributions are welcome. Feel free to open issues or pull requests.
