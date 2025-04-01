// Package ebus provides a generic event bus with support for transactional dispatching,
// middleware, and payload lifecycle (validate, handle, commit, rollback).
// author Marcin Maluszczak
package ebus

import (
	"encoding/binary"
	"github.com/pkg/errors"
	"strconv"
	"sync/atomic"
	"time"
)

type PayloadType int

// EventHandler is a generic function that handles an event with a given context.
type EventHandler[T, ID any] func(ctx T, event *Event[T, ID]) error

// CommitListener is a function that is triggered after a payload has been committed.
type CommitListener[T any] func(ctx T, payload Payload[T])

// EventMiddleware wraps an EventHandler to add cross-cutting behavior like logging or recovery.
type EventMiddleware[T, ID any] func(next EventHandler[T, ID]) EventHandler[T, ID]

// IDGenHandler is generic function that handles creation ID for new event
type IDGenHandler[T any, ID any] func(ctx T) (ID, error)

// Event represents a single event containing multiple payloads.
type Event[T, ID any] struct {
	ID       ID           // Unique identifier of the event
	Created  time.Time    // Timestamp of event creation
	Payloads []Payload[T] // A list of payloads included in the event
}

// Payload defines a lifecycle for event actions including validation, handling, rollback, and commit.
type Payload[T any] interface {
	Validate(ctx T) error
	Commit(ctx T)
	Handle(ctx T) error
	Rollback(ctx T)
	PayloadType() PayloadType
}

// TxHandler defines a function that executes a given handler within a transaction.
type TxHandler[T any] func(ctx T, handler func() error) error

// NewDefault creates a Bus with the default dispatcher and optional middleware.
func NewDefault[T, ID any](idGenHandler IDGenHandler[T, ID], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	return New[T, ID](idGenHandler, Dispatcher[T, ID], middlewares...)
}

// NewWithTx creates a Bus that wraps dispatch logic in a transaction using the provided TxHandler.
func NewWithTx[T any, ID any](idGenHandler IDGenHandler[T, ID], txHandler TxHandler[T], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	return New(idGenHandler, TxDispatcher[T, ID](txHandler), middlewares...)
}

// New creates a new Bus with a given event handler and middleware chain.
func New[T, ID any](idGenHandler IDGenHandler[T, ID], handler EventHandler[T, ID], middlewares ...EventMiddleware[T, ID]) *Bus[T, ID] {
	b := &Bus[T, ID]{
		idGenHandler: idGenHandler,
		handlers:     make(map[string]EventHandler[T, ID]),
	}
	b.middlewares = applyMiddleware(handler, middlewares...)
	return b
}

// Bus represents the core event bus with middleware, payload dispatching, and handler registration.
type Bus[T, ID any] struct {
	idGenHandler IDGenHandler[T, ID]
	middlewares  EventHandler[T, ID]
	handlers     map[string]EventHandler[T, ID]
}

// ID8Byte is a fixed-size 8-byte identifier.
type ID8Byte [8]byte

// NewID8ByteHandler returns a thread-safe ID generator that produces sequential ID8Byte values.
// The counter is unique per handler instance.
func NewID8ByteHandler[T any]() IDGenHandler[T, ID8Byte] {
	var counter uint64
	return func(ctx T) (ID8Byte, error) {
		val := atomic.AddUint64(&counter, 1)
		var out ID8Byte
		binary.BigEndian.PutUint64(out[:], val)
		return out, nil
	}
}

// Publish creates a new Event and applies it to the bus with Timestamp in UTC
func (b *Bus[T, ID]) Publish(ctx T, events []Payload[T]) error {
	if len(events) == 0 {
		return ErrEmptyPayloads
	}

	id, err := b.idGenHandler(ctx)
	if err != nil {
		return err
	}

	e := Event[T, ID]{
		ID:       id,
		Created:  time.Now().UTC(),
		Payloads: events,
	}

	return b.Apply(ctx, &e)
}

// CommandEvent is a special Payload that produces a result value after execution.
type CommandEvent[T, Res any] interface {
	Payload[T]
	Result() Res
}

// Run publishes a command event and extracts its result.
func Run[T any, ID any, Res any](ctx T, bus *Bus[T, ID], payloads []Payload[T]) (Res, error) {
	var zero Res
	if len(payloads) == 0 {
		return zero, ErrEmptyPayloads
	}

	cmd, ok := payloads[0].(CommandEvent[T, Res])
	if !ok {
		return zero, ErrPayloadIsNotCommand
	}

	if err := bus.Publish(ctx, payloads); err != nil {
		return zero, err
	}
	return cmd.Result(), nil
}

// Apply applies an event using the configured middleware chain.
func (b *Bus[T, ID]) Apply(ctx T, event *Event[T, ID]) error {
	return b.middlewares(ctx, event)
}

// applyMiddleware chains the middleware around the main event handler.
func applyMiddleware[T, ID any](handler EventHandler[T, ID],
	middlewares ...EventMiddleware[T, ID]) EventHandler[T, ID] {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// Subscribers manages commit listeners for specific payload types.
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
		return func(ctx T, event *Event[T, ID]) error {
			if err := next(ctx, event); err != nil {
				return err
			}

			for _, p := range event.Payloads {
				if subs := s.listeners[p.PayloadType()]; subs != nil {
					for _, fn := range subs {
						fn(ctx, p)
					}
				}
			}

			return nil
		}
	}
}

// TxDispatcher wraps the dispatch logic in a transactional context.
func TxDispatcher[T, ID any](inTx TxHandler[T]) EventHandler[T, ID] {
	return func(ctx T, event *Event[T, ID]) error {
		if event == nil || len(event.Payloads) == 0 {
			return ErrEmptyPayloads
		}

		if err := validateAll(ctx, event.Payloads); err != nil {
			return err
		}

		err := inTx(ctx, func() error {
			return applyAll(ctx, event.Payloads)
		})

		if err != nil {
			return err
		}

		commitAll(ctx, event.Payloads)
		return nil
	}
}

// Dispatcher is the default non-transactional dispatcher.
func Dispatcher[T, ID any](ctx T, event *Event[T, ID]) error {
	if event == nil || len(event.Payloads) == 0 {
		return ErrEmptyPayloads
	}
	if err := validateAll(ctx, event.Payloads); err != nil {
		return err
	}
	if err := applyAll(ctx, event.Payloads); err != nil {
		return err
	}
	commitAll(ctx, event.Payloads)
	return nil
}

type PayloadTypeNameHandler func(pt PayloadType) string

func LoggingById[T any, ID any](logf func(format string, args ...any)) EventMiddleware[T, ID] {
	return LoggingByTypeName[T, ID](func(pt PayloadType) string {
		return strconv.FormatInt(int64(pt), 10)
	}, logf)
}

func LoggingByTypeName[T any, ID any](payloadTypeNameHandler PayloadTypeNameHandler, logf func(format string, args ...any)) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(ctx T, event *Event[T, ID]) error {
			logf("→ Event %v started with %d payloads", event.ID, len(event.Payloads))
			for _, p := range event.Payloads {
				logf("   ↪ Payload type: %s", payloadTypeNameHandler(p.PayloadType()))
			}

			start := time.Now()
			err := next(ctx, event)
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

// RetryPolicy Interface
type RetryPolicy interface {
	ShouldRetry(attempt int, err error) bool
	Backoff(attempt int) time.Duration
}

// RetryWithPolicy retries event dispatch based on a custom retry policy.
// Allows for conditional retry logic and custom backoff strategies.
func RetryWithPolicy[T, ID any](policy RetryPolicy) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(ctx T, event *Event[T, ID]) error {
			var err error
			attempt := 0
			for {
				err = next(ctx, event)
				if err == nil || !policy.ShouldRetry(attempt, err) {
					return err
				}
				time.Sleep(policy.Backoff(attempt))
				attempt++
			}
		}
	}
}

// RetryFallbackHandler defines a function that receives failed events.
type RetryFallbackHandler[T any, ID any] func(evt *Event[T, ID], err error)

// RetryWithFallback retries event dispatch using a policy and calls fallback on failure.
func RetryWithFallback[T, ID any](policy RetryPolicy, fallback RetryFallbackHandler[T, ID]) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(ctx T, event *Event[T, ID]) error {
			var err error
			attempt := 0
			for {
				err = next(ctx, event)
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

type EventHooks[T, ID any] struct {
	OnBeforeHandle func(ctx T, event *Event[T, ID])
	OnAfterCommit  func(ctx T, event *Event[T, ID])
}

// WithEventHooks allows injecting callbacks before handling and after committing an event.
// Useful for logging, metrics, or tracing lifecycle stages.
func WithEventHooks[T, ID any](hooks EventHooks[T, ID]) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(ctx T, event *Event[T, ID]) error {
			if hooks.OnBeforeHandle != nil {
				hooks.OnBeforeHandle(ctx, event)
			}
			err := next(ctx, event)
			if err == nil && hooks.OnAfterCommit != nil {
				hooks.OnAfterCommit(ctx, event)
			}
			return err
		}
	}
}

// Retry middleware that forces the event to repeat when it fails
func Retry[T, ID any](retries int, delay time.Duration) EventMiddleware[T, ID] {
	return func(next EventHandler[T, ID]) EventHandler[T, ID] {
		return func(ctx T, event *Event[T, ID]) error {
			var err error
			for i := 0; i < retries; i++ {
				err = next(ctx, event)
				if err == nil {
					return nil
				}
				time.Sleep(delay)
			}
			return err
		}
	}
}

// validateAll checks if all payloads pass validation.
func validateAll[T any](ctx T, payloads []Payload[T]) error {
	for _, e := range payloads {
		if err := e.Validate(ctx); err != nil {
			return err
		}
	}
	return nil
}

// applyAll runs the Handle method for each payload and rolls back on error.
func applyAll[T any](ctx T, payloads []Payload[T]) error {
	for i, e := range payloads {
		if err := e.Handle(ctx); err != nil {
			for j := i; j >= 0; j-- {
				payloads[j].Rollback(ctx)
			}
			return err
		}
	}
	return nil
}

// commitAll commits all payloads after successful processing.
func commitAll[T any](ctx T, payloads []Payload[T]) {
	for _, p := range payloads {
		p.Commit(ctx)
	}
}

// Common errors
var ErrEmptyPayloads = errors.New("empty payloads")
var ErrPayloadIsNotCommand = errors.New("payload is not command or not match type")
