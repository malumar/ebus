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

For more exaples look into ebus_test.go

#### With result:

```go
res, err := ebus.Run[AppCtx, ebus.ID8Byte, int](ctx, bus, []ebus.Payload[AppCtx]{
    &EventWithResult{I: 42},
})
```

---

### 🧩 Payload Lifecycle

Each payload must implement the following interface:

```go
type Payload[T any] interface {
    Validate(ctx T) error
    Handle(ctx T) error
    Commit(ctx T)
    Rollback(ctx T)
    PayloadType() PayloadType
}
```

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

```go
bus := ebus.NewDefault(idGen,
    myCustomLogger,
    myTracingLayer,
)
```

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
