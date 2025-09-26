// Package ebus provides a generic event bus with support for transactional dispatching,
// middleware, and payload lifecycle (validate, handle, commit, rollback).
// author Marcin Maluszczak
package ebus

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strconv"
	"sync/atomic"
	"time"
)

// PayloadType identifies a concrete payload kind. It is used for decoding,
// routing and subscription to payload-specific listeners.
type PayloadType int

// EventHandler processes an Event in a given environment T. Returning a non-nil
// error fails the entire event (and triggers rollback where applicable).
type EventHandler[T, ID any] func(env T, event *Event[T, ID]) error

// CommitListener is invoked after a payload has been successfully committed.
// It is called once per committed payload of the subscribed type.
type CommitListener[T any] func(env T, payload Payload[T])

// EventMiddleware decorates an EventHandler with cross-cutting behavior such as
// logging, metrics, retries or panic recovery.
type EventMiddleware[T, ID any] func(next EventHandler[T, ID]) EventHandler[T, ID]

// IDGenHandler generates a unique identifier for a new event using the provided
// environment T. Errors prevent event creation.
type IDGenHandler[T any, ID any] func(env T) (ID, error)

// Event groups multiple payloads to be processed together (atomically when
// using a transactional dispatcher). Created is set to UTC time at publish.
type Event[T, ID any] struct {
	ID       ID           // Unique identifier of the event
	Created  time.Time    // Timestamp of event creation
	Payloads []Payload[T] // A list of payloads included in the event
}

// Payload defines the lifecycle of a unit of work processed by the bus.
// Validate should check invariants, Handle applies the change, Commit finalizes
// successful changes and Rollback reverts partial work on failure.
type Payload[T any] interface {
	Validate(env T) error
	Commit(env T)
	Handle(env T) error
	Rollback(env T)
	PayloadType() PayloadType
}

// Stager can be implemented by a transaction to receive the RAW form of payloads
// before handling (e.g., for persistence, audit, or outbox purposes).
// Stager can be implemented in Tx, remember that additionally your Payload must implement RawKeeper
type Stager[ID any] interface {
	PutRaw(raw *Raw[ID]) error
}

// Tx represents a transactional boundary used by the transactional dispatcher.
// Commit finalizes changes; Rollback reverts them.
// if Tx has Stager implemented, it will receive Payload at the time of opening tx,
// remember that additionally your Payload must implement RawKeeper
type Tx interface {
	Rollback() error
	Commit() error
}

// Transactional is implemented by environments that can begin a transaction.
// The readonly flag can be used by implementations to optimize or enforce mode.
type Transactional interface {
	BeginTx(readonly bool) (Tx, error)
}

// Raw carries the original serialized payload and transport metadata as received
// before decoding. Body contains the payload bytes and is transient; Clone it if
// you need to persist it beyond processing. Meta is populated by the bus when
// publishing: EventID and TimestampUnix are set from the event; CorrelationID
// and SchemaVer can be used for routing and schema/versioning.
//
// When a payload implements RawKeeper[ID], the bus injects a pointer to its Raw
// before Handle is invoked. If the active transaction implements Stager[ID],
// PutRaw is called to stage the RAW envelope before handling.
type Raw[ID any] struct {
	Type PayloadType
	// Body will disappear after the process is over, so if you want to save it somewhere,
	// Clone it, e.g. through the Clone function
	Body []byte
	Meta struct {
		EventID       ID
		CorrelationID [16]byte
		SchemaVer     uint16
		TimestampUnix int64
	}
}

// RawKeeper can be implemented by payloads that want access to the incoming RAW
// representation. When publishing RAWs, the bus populates RawKeeper automatically.
// RawKeeper if you want to receive PayLoad in the form that came with the Event,
// implement RawKeeper or use RawPayload as the basis of the structure
type RawKeeper[ID any] interface {
	// SetRaw sets the underlying RAW pointer. The bus calls this when publishing RAWs
	// to pass the original envelope to the payload.
	SetRaw(raw *Raw[ID])
	// Raw returns the stored RAW pointer, which may be nil if not published from RAW input.
	Raw() *Raw[ID]
}

// RawPayload is a helper that implements RawKeeper. Embed it into payload structs
// to receive the original RAW value when publishing RAWs.
type RawPayload[ID any] struct {
	raw *Raw[ID] `json:"-"`
}

// SetRaw sets the underlying RAW pointer. The bus calls this when publishing RAWs
// to pass the original envelope to the payload
func (p *RawPayload[ID]) SetRaw(r *Raw[ID]) { p.raw = r }

// Raw returns the stored RAW pointer, which may be nil if not published from RAW input.
func (p *RawPayload[ID]) Raw() *Raw[ID] { return p.raw }

// Decoder converts a (payload type, raw bytes) pair into a concrete Payload[T].
// Implementations should return an error for unknown types or malformed data.
type Decoder[T any] interface {
	Decode(pt PayloadType, raw []byte) (Payload[T], error)
}

// NewJSONDecoder returns a JSONDecoder with an empty Registry.
// Register concrete payload factories on the embedded Registry before decoding.
func NewJSONDecoder[T any]() *JSONDecoder[T] {
	return &JSONDecoder[T]{Registry: NewRegistry[T]()}
}

// Registry maps PayloadType to factory functions that construct zero-value
// payload instances for decoding. It also tracks registration origins to aid debugging.
type Registry[T any] struct {
	registry map[PayloadType]func() Payload[T]
	origins  map[PayloadType]string // from where it was first recorded (file:line)
}

// NewRegistry returns a new, empty Registry for mapping PayloadType to factories.
func NewRegistry[T any]() Registry[T] {
	return Registry[T]{
		registry: make(map[PayloadType]func() Payload[T]),
		origins:  make(map[PayloadType]string),
	}
}

// MustRegister registers a factory for the given payload type.
// It panics if the type was already registered. Use Register to get an error instead.
func (r *Registry[T]) MustRegister(payloadType PayloadType, factory func() Payload[T]) {
	if err := r.Register(payloadType, factory); err != nil {
		panic(err)
	}
}

// Register associates a payload type with a factory function used during decoding.
// Returns an error if the payload type is already registered.
func (r *Registry[T]) Register(payloadType PayloadType, factory func() Payload[T]) error {
	if _, found := r.registry[payloadType]; found {
		return fmt.Errorf("payload factory %v already registered (first at %s)", payloadType, r.origins[payloadType])
	}
	// save Origin
	r.origins[payloadType] = captureOrigin(1)
	r.registry[payloadType] = factory
	return nil
}

// ContextCarrier can be implemented by environment types to expose a context.Context,
// enabling middleware to honor cancellation and deadlines.
// Context-respecting middleware should use ContextCarrier
// Example Env T:
//
//	type Env struct {
//		ctx context.Context
//		...other fields and implementations (Transactional, etc.)
//	 }
//
// func (s AppState) Context() context.Context { return s.ctx }
// e.g.
// ctx := context.Background()
//
//	if c, ok := any(env).(ContextCarrier); ok && c.Context() != nil {
//	   ctx = c.Context()
//	}
type ContextCarrier interface {
	Context() context.Context
}

// JSONDecoder decodes payloads from JSON using a type Registry. Unknown fields
// are disallowed to surface schema mismatches early.
type JSONDecoder[T any] struct {
	Registry[T]
}

// Decode implements Decoder by constructing a payload from the registered factory,
// disallowing unknown JSON fields, and decoding the provided bytes into it.
// Returns an error for unknown types or malformed JSON.
func (d *JSONDecoder[T]) Decode(pt PayloadType, raw []byte) (Payload[T], error) {
	newP, ok := d.registry[pt]
	if !ok {
		return nil, fmt.Errorf("unknown payload type: %v", pt)
	}

	p := newP()

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return nil, err
	}

	return p, nil
}

// NewDefault constructs a Bus that uses the default non-transactional Dispatcher.
// Middlewares are applied around the handler in the provided order.
func NewDefault[T, ID any](idGenHandler IDGenHandler[T, ID], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	return New[T, ID](idGenHandler, Dispatcher[T, ID], middlewares...)
}

// NewWithTx constructs a Bus that runs dispatch inside a transaction created by env.
// If the begun Tx implements Stager[ID], RAW payloads are staged before Handle.
// See Transactional and Stager for details.
// Requirements:
//   - T must implement Transactional (i.e., provide BeginTx to start a transaction)
//   - The returned Tx may optionally implement Stager[ID]; if it does, RAW payloads
//     (via RawKeeper[ID]) will be staged before handling. Stager is detected dynamically
//     using a type assertion.
//
// In short: T starts the transaction; if Tx can stage, the Bus will stage RAWs first,
// then run the Validate/Handle/Commit/Rollback lifecycle under the transaction.
func NewWithTx[T Transactional, ID any](idGenHandler IDGenHandler[T, ID], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	return New(idGenHandler, TxDispatcher[T, ID](), middlewares...)
}

// New constructs a Bus with a custom root handler and a middleware chain.
// Middlewares are composed outermost-first in the order they are provided.
func New[T, ID any](idGenHandler IDGenHandler[T, ID], handler EventHandler[T, ID], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	b := &Bus[T, ID]{
		idGenHandler: idGenHandler,
	}
	b.middlewares = applyMiddleware(handler, middlewares...)
	return b
}

// Bus is the core event bus. It holds the ID generator and the composed middleware
// chain that ultimately dispatches events to a handler.
type Bus[T, ID any] struct {
	idGenHandler IDGenHandler[T, ID]
	middlewares  EventHandler[T, ID]
	//pipelineBuilder PipelineBuilder[T, ID]
}

// ID8Byte is a fixed-size, big-endian 8-byte identifier suitable for compact,
// sortable event IDs.
type ID8Byte [8]byte

// NewID8ByteHandler returns a thread-safe ID generator that yields sequential,
// big-endian 8-byte identifiers. Values start from initialValue+1 for each call.
// The counter is unique per handler instance.
func NewID8ByteHandler[T any](initialValue uint64) IDGenHandler[T, ID8Byte] {
	var counter = initialValue
	return func(env T) (ID8Byte, error) {
		val := atomic.AddUint64(&counter, 1)
		var out ID8Byte
		binary.BigEndian.PutUint64(out[:], val)
		return out, nil
	}
}

// Publish creates an Event with a single payload, sets Created to UTC, and applies it.
func (b *Bus[T, ID]) Publish(env T, payload Payload[T]) error {
	return b.PublishAll(env, []Payload[T]{payload})
}

// PublishRaw decodes a single RAW payload (type + bytes) using the provided Decoder,
// then publishes it as an event. Useful for ingesting serialized messages.
func (b *Bus[T, ID]) PublishRaw(
	env T,
	dec Decoder[T],
	pt PayloadType,
	body []byte,
) error {
	return b.PublishRaws(env, dec, Raw[ID]{Type: pt, Body: body})
}

// PublishRaws decodes all provided RAW payloads first. If any decode fails,
// no changes are applied. With a transactional bus, all payloads are processed
// within a single transaction.
func (b *Bus[T, ID]) PublishRaws(
	env T,
	dec Decoder[T],
	raws ...Raw[ID],
) error {

	if len(raws) == 0 {
		return nil
	}

	// 1) Decode EVERYTHING in memory before you start any Handle(). Thanks to this,
	// TxDispatcher will cover the whole thing – there will be no partial success.
	payloads := make([]Payload[T], 0, len(raws))
	for i := range raws {
		p, err := dec.Decode(raws[i].Type, raws[i].Body)
		if err != nil {
			return fmt.Errorf("decode[%d]: %w", i, err)

		}
		if rawKeeper, ok := p.(RawKeeper[ID]); ok {
			rawKeeper.SetRaw(&raws[i])
		}
		payloads = append(payloads, p)
	}

	// 2) Regular publish on decoded payloads – the whole cycle works:
	// Validate -> Handle -> (Commit|Rollback), under the control of TxDispatcher
	return b.PublishAll(env, payloads)
}

// PublishAll creates an Event with the given payloads, timestamps it in UTC,
// and applies it through the bus. Either all payloads are processed or none.
func (b *Bus[T, ID]) PublishAll(env T, events []Payload[T]) error {
	if len(events) == 0 {
		return ErrEmptyPayloads
	}

	id, err := b.idGenHandler(env)
	if err != nil {
		return err
	}

	e := Event[T, ID]{
		ID:       id,
		Created:  time.Now().UTC(),
		Payloads: events,
	}

	return b.Apply(env, &e)
}

// CommandEvent is a special Payload that produces a result value after execution.
type CommandEvent[T, Res any] interface {
	Payload[T]
	Result() Res
}

// Run publishes a single command payload and returns its result.
// The payload must implement CommandEvent[T, Res].
func Run[T any, ID any, Res any](env T, bus *Bus[T, ID], payload Payload[T]) (Res, error) {
	return RunAll[T, ID, Res](env, bus, []Payload[T]{payload})
}

// RunAll publishes multiple payloads and returns the result from the first one,
// which must implement CommandEvent[T, Res]. All payloads are processed together.
func RunAll[T any, ID any, Res any](env T, bus *Bus[T, ID], payloads []Payload[T]) (Res, error) {
	var zero Res
	if len(payloads) == 0 {
		return zero, ErrEmptyPayloads
	}

	cmd, ok := payloads[0].(CommandEvent[T, Res])
	if !ok {
		return zero, ErrPayloadIsNotCommand
	}

	if err := bus.PublishAll(env, payloads); err != nil {
		return zero, err
	}
	return cmd.Result(), nil

}

// Apply dispatches the given event through the composed middleware chain and handler.
func (b *Bus[T, ID]) Apply(env T, event *Event[T, ID]) error {
	return b.middlewares(env, event)
}

// applyMiddleware chains the middleware around the main event handler.
func applyMiddleware[T, ID any](handler EventHandler[T, ID],
	middlewares ...EventMiddleware[T, ID]) EventHandler[T, ID] {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// Subscribers registers a CommitListener for the payload's type.
// The listener is invoked after a successful commit for matching payloads.
//
// Concurrency model:
//   - Subscribe is intended to be called only during application startup (single-threaded),
//     before any events are processed.
//   - After startup, the set of listeners is treated as immutable; Notifier may be used
//     concurrently without additional synchronization.
//   - Do NOT call Subscribe at runtime.
//
// Rationale: avoiding locks in the hot path (e.g., inside Tx dispatch) keeps latency low.
type Subscribers[T, ID any] struct {
	listeners map[PayloadType][]CommitListener[T]
}

// NewSubscribers creates an empty list of subscribers.
func NewSubscribers[T, ID any]() *Subscribers[T, ID] {
	return &Subscribers[T, ID]{
		listeners: make(map[PayloadType][]CommitListener[T]),
	}
}

// Subscribe registers a commit listener for a specific payload.
func (s *Subscribers[T, ID]) Subscribe(payload Payload[T], handler CommitListener[T]) {
	pt := payload.PayloadType()
	s.listeners[pt] = append(s.listeners[pt], handler)
}

// Notifier returns an EventMiddleware that notifies all matching subscribers after commit.
func (s *Subscribers[T, ID]) Notifier() EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			if err := next(env, event); err != nil {
				return err
			}

			for _, p := range event.Payloads {
				if subs := s.listeners[p.PayloadType()]; subs != nil {
					for _, fn := range subs {
						fn(env, p)
					}
				}
			}

			return nil
		}
	}
}

// TxDispatcher returns an EventHandler that validates payloads, begins a transaction,
// optionally stages RAWs if the Tx implements Stager[ID], handles payloads,
// commits on success, or rolls back (with per-payload Rollback) on failure or panic.
func TxDispatcher[T Transactional, ID any]() EventHandler[T, ID] {

	return func(env T, event *Event[T, ID]) (err error) {

		if err = ValidateAll(env, event); err != nil {
			return err
		}

		tx, err := env.BeginTx(false)
		if err != nil {
			return err
		}

		handled := 0 // Number of completed Handles (precise rollback)
		defer func() {
			if r := recover(); r != nil {
				_ = tx.Rollback()
				// Undo the changes also for the payload that panicked:
				RollbackRangeUnsafe(env, event, 0, handled+1)
				err = fmt.Errorf("panic: %v", r)
			}
		}()

		// staging + handle; modification of ApplyAllUnsafe to return handled and not crash
		handled, err = ApplyAllUnsafe(env, event, tx)
		if err != nil {
			// also close tx
			if txr := tx.Rollback(); txr != nil {
				log.Printf("ebus: TxDispatcher Rollback error %v", txr)
			}
			RollbackRangeUnsafe(env, event, 0, handled+1) // +1
			return err
		}

		if err = tx.Commit(); err != nil {
			// payload data did not compromise – we are canceling reservations in RAM
			if txr := tx.Rollback(); txr != nil {
				log.Printf("ebus: TxDispatcher Rollback error %v", txr)
			}
			RollbackRangeUnsafe(env, event, 0, handled)
			return err
		}

		CommitAllUnsafe(env, event)
		return nil
	}
}

// Dispatcher is the default non-transactional handler that validates,
// handles all payloads, rolls back on failure, and commits on success.
func Dispatcher[T, ID any](env T, event *Event[T, ID]) error {

	if err := ValidateAll(env, event); err != nil {
		return err
	}
	nHandled, err := ApplyAllUnsafe(env, event, nil)
	if err != nil {
		// Undo changes for all completed + currently incorrect
		RollbackRangeUnsafe(env, event, 0, nHandled+1)
		return err
	}

	CommitAllUnsafe(env, event)
	return nil
}

// PayloadTypeNameHandler returns a human-friendly name for a given PayloadType,
// typically used by logging middleware.
type PayloadTypeNameHandler func(pt PayloadType) string

// LoggingByID returns logging middleware that identifies payload types by their numeric ID.
func LoggingByID[T any, ID any](logf func(format string, args ...any)) EventMiddleware[T, ID] {
	return LoggingByTypeName[T, ID](func(pt PayloadType) string {
		return strconv.FormatInt(int64(pt), 10)
	}, logf)
}

// LoggingByTypeName returns logging middleware that uses the provided type-name resolver
// to print human-friendly payload type names.
func LoggingByTypeName[T any, ID any](payloadTypeNameHandler PayloadTypeNameHandler, logf func(format string, args ...any)) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			logf("→ Event %v started with %d payloads", event.ID, len(event.Payloads))
			for _, p := range event.Payloads {
				logf("   ↪ Payload type: %s", payloadTypeNameHandler(p.PayloadType()))
			}

			start := time.Now()
			err := next(env, event)
			duration := time.Since(start)

			if err != nil {
				logf("✖ Event %v failed after %s: %v", event.ID, duration, err)
			} else {
				logf("✓ Event %v handled successfully in %s", event.ID, duration)
			}
			return err
		}
	}
}

// RetryWithContext retries dispatch up to retries times with a fixed delay.
// If env exposes a context (via a Context() context.Context method), cancellation
// and deadlines are honored between attempts.
// Context-respecting middleware should use ContextCarrier
func RetryWithContext[T any, ID any](retries int, delay time.Duration) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, evt *Event[T, ID]) error {
			ctx := context.Background()
			if c, ok := any(env).(ContextCarrier); ok && c.Context() != nil {
				ctx = c.Context()
			}
			var err error
			for i := 0; i < retries; i++ {
				select {
				case <-ctx.Done():
					return fmt.Errorf("canceled: %w", ctx.Err())
				default:
				}
				if err = next(env, evt); err == nil {
					return nil
				}
				if i < retries-1 {
					t := time.NewTimer(delay)
					select {
					case <-ctx.Done():
						t.Stop()
						return fmt.Errorf("canceled: %w", ctx.Err())
					case <-t.C:
					}
				}
			}
			return err
		}
	}
}

// RetryPolicy RetryWithPolicy retries dispatch according to the provided RetryPolicy,
// sleeping according to Backoff between attempts and stopping when ShouldRetry returns false.
type RetryPolicy interface {
	ShouldRetry(attempt int, err error) bool
	Backoff(attempt int) time.Duration
}

// RetryWithPolicy retries event dispatch based on a custom retry policy.
// Allows for conditional retry logic and custom backoff strategies.
func RetryWithPolicy[T, ID any](policy RetryPolicy) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			var err error
			attempt := 0
			for {
				err = next(env, event)
				if err == nil || !policy.ShouldRetry(attempt, err) {
					return err
				}
				time.Sleep(policy.Backoff(attempt))
				attempt++
			}
		}
	}
}

// ExpoJitterPolicy Without jitter, multiple instances will start retrying at the same intervals
// (thundering herd), which can overload the system (next wave). Jitter randomizes latency within
// a certain range, dissipating the load.
// example:
// policy := &ExpoJitterPolicy{Base: 10*time.Millisecond, Factor: 2, Max: 500*time.Millisecond}
// mw := RetryWithPolicy[testCtx, int](policy)
type ExpoJitterPolicy struct {
	Base   time.Duration // eg. 10 * time.Millisecond
	Factor float64       // eg. 2.0 (2^attempt)
	Max    time.Duration // maximum delay
	Rand   *rand.Rand    // optional;  If NIL, use the global
}

func (p *ExpoJitterPolicy) ShouldRetry(attempt int, err error) bool { return true }

func (p *ExpoJitterPolicy) Backoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	mult := math.Pow(p.Factor, float64(attempt))
	dur := time.Duration(float64(p.Base) * mult)
	if p.Max > 0 && dur > p.Max {
		dur = p.Max
	}
	// full jitter in [0, dur)
	r := p.Rand
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if dur <= 0 {
		return 0
	}
	return time.Duration(r.Int63n(int64(dur)))
}

// RetryFallbackHandler receives the final error after retries are exhausted
// together with the event that failed to be processed.
type RetryFallbackHandler[T any, ID any] func(evt *Event[T, ID], err error)

// RetryWithFallback retries using the given policy and invokes fallback with the final
// error if all attempts fail.
func RetryWithFallback[T, ID any](policy RetryPolicy, fallback RetryFallbackHandler[T, ID]) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			var err error
			attempt := 0
			for {
				err = next(env, event)
				if err == nil || !policy.ShouldRetry(attempt, err) {
					break
				}
				time.Sleep(policy.Backoff(attempt))
				attempt++
			}
			if err != nil && fallback != nil {
				fallback(event, err)
			}
			return err
		}
	}
}

// EventHooks provides observational callbacks for event processing lifecycle:
// OnBeforeHandle is called before handling, OnAfterCommit after a successful commit.
type EventHooks[T, ID any] struct {
	OnBeforeHandle func(env T, event *Event[T, ID])
	OnAfterCommit  func(env T, event *Event[T, ID])
}

// WithEventHooks injects observational callbacks that run before handling and
// after a successful commit. Hooks must be side-effect free.
func WithEventHooks[T, ID any](hooks EventHooks[T, ID]) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			if hooks.OnBeforeHandle != nil {
				hooks.OnBeforeHandle(env, event)
			}
			err := next(env, event)
			if err == nil && hooks.OnAfterCommit != nil {
				hooks.OnAfterCommit(env, event)
			}
			return err
		}
	}
}

// Retry retries dispatch up to retries times with a fixed delay. This variant
// does not honor context cancellation; prefer RetryWithContext when applicable.
func Retry[T, ID any](retries int, delay time.Duration) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, event *Event[T, ID]) error {
			var err error
			for i := 0; i < retries; i++ {
				err = next(env, event)
				if err == nil {
					return nil
				}
				time.Sleep(delay)
			}
			return err
		}
	}
}

// Recovery wraps the handler and converts panics into errors, optionally logging
// the panic using the provided formatter.
func Recovery[T, ID any](logf func(format string, args ...any)) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(env T, evt *Event[T, ID]) (err error) {
			defer func() {
				if r := recover(); r != nil {
					if logf != nil {
						logf("panic recovered in event %v: %v", evt.ID, r)
					}
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return next(env, evt)
		}
	}
}

// ValidateAll verifies that the event and all payloads pass validation.
// Returns ErrEmptyPayloads when the event has no payloads.
func ValidateAll[T, ID any](env T, event *Event[T, ID]) error {

	if event == nil || len(event.Payloads) == 0 {
		return ErrEmptyPayloads
	}

	for _, e := range event.Payloads {
		if err := e.Validate(env); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAllUnsafe handles each payload in order and returns the number of successfully
// handled payloads along with the first error encountered. If tx implements Stager[ID],
// RAW payloads kept by RawKeeper are staged before Handle. Panic-safe: converts panics
// from Handle into an error and returns the count of completed payloads.
// Unsafe: assumes ValidateAll was run.
func ApplyAllUnsafe[T, ID any](env T, event *Event[T, ID], tx Tx) (handled int, err error) {
	// Convert panics during handling into an error, preserving the handled count.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	var stager Stager[ID]
	if tx != nil {
		if st, ok := any(tx).(Stager[ID]); ok {
			stager = st
		}
	}

	for i := range event.Payloads {
		handled = i // number of payloads completed before the current one
		p := event.Payloads[i]

		if rk, ok := p.(RawKeeper[ID]); ok && rk.Raw() != nil {
			rk.Raw().Meta.EventID = event.ID
			rk.Raw().Meta.TimestampUnix = event.Created.Unix()
			if stager != nil {
				if e := stager.PutRaw(rk.Raw()); e != nil {
					err = fmt.Errorf("stage raw (type=%v): %w", p.PayloadType(), e)
					return
				}
			}
		}

		if err = p.Handle(env); err != nil {
			return
		}
	}

	handled = len(event.Payloads)
	return
}

// RollbackRangeUnsafe calls Rollback on payloads in reverse order from index (to-1) down to from.
// Unsafe: assumes bounds are valid and validation has already occurred.
func RollbackRangeUnsafe[T, ID any](env T, ev *Event[T, ID], from, to int) {
	for i := to - 1; i >= from; i-- {
		ev.Payloads[i].Rollback(env)
	}
}

// CommitAllUnsafe calls Commit on all payloads after successful processing.
// Unsafe: assumes validation and handling have succeeded.
func CommitAllUnsafe[T, ID any](env T, event *Event[T, ID]) {
	for _, p := range event.Payloads {
		p.Commit(env)
	}
}

func Clone(b []byte) []byte { c := make([]byte, len(b)); copy(c, b); return c }

// Common errors

// ErrEmptyPayloads indicates that an event contained no payloads to process.
var ErrEmptyPayloads = errors.New("empty payloads")

// ErrPayloadIsNotCommand indicates the payload does not implement the CommandEvent
// interface or does not match the expected result type.
var ErrPayloadIsNotCommand = errors.New("payload is not a command or result type mismatch")
