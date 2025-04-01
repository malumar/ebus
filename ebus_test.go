package ebus

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"strings"
	"testing"
	"time"
)

const (
	UserPayload PayloadType = iota
	EventWithResultPayload
)

type EventWithResult struct {
	i         int
	randValue int
}

func (self *EventWithResult) Result() int {
	return self.randValue
}

func (self *EventWithResult) Validate(ctx AppCtx) error {
	if self.i == 0 {
		return errors.New("invalid i value")
	}
	return nil
}
func (self *EventWithResult) Commit(ctx AppCtx) {
	ctx.SetInt(self.i, self.randValue)
}

func (self *EventWithResult) Rollback(ctx AppCtx) {
	self.randValue = 0
}

func (self *EventWithResult) PayloadType() PayloadType {
	return EventWithResultPayload
}

func (self *EventWithResult) Handle(ctx AppCtx) error {
	self.randValue = rand.Intn(10)

	if ctx.IntExists(self.i * self.randValue) {
		return errors.New("duplicated int")
	}
	return nil
}

type UserData struct {
	Login    string
	age      int
	reserved bool
}

func (self *UserData) Validate(ctx AppCtx) error {
	if self.Login == "" {
		return fmt.Errorf("missing login")
	}

	if ctx.UserExists(self.Login) {
		return fmt.Errorf("duplicated login " + self.Login)
	}

	return nil
}
func (self *UserData) Commit(ctx AppCtx) {
	ctx.Put(self.Login, self.age)
}

func (self *UserData) Rollback(ctx AppCtx) {

	if self.reserved {
		ctx.Logf("rolling back, delete user %s, exists = %v\n", self.Login, ctx.UserExists(self.Login))
		ctx.DeleteUser(self.Login)
	}
}

func (self *UserData) PayloadType() PayloadType {
	return UserPayload
}

func (self *UserData) Handle(ctx AppCtx) error {
	self.reserved = self.age > 130
	if self.reserved {
		ctx.Put(self.Login, self.age)
		return errors.New("not possible")
	}
	return nil
}

type SimpleCtx struct {
	users map[string]*User
	ints  map[int]int
	bus   *Bus[AppCtx, ID8Byte]
	logf  func(format string, args ...any)
}

type User struct {
	Login  string
	Number int
}

func (self *SimpleCtx) Logf(format string, args ...any) {
	self.logf(format, args...)
}
func (self *SimpleCtx) UserExists(username string) bool {
	if self.users == nil {
		return false
	}
	if _, ok := self.users[username]; ok {
		return true
	}

	return false
}

func (self *SimpleCtx) SetInt(key, multiplier int) {
	if self.ints == nil {
		self.ints = make(map[int]int)
	}

	self.ints[key] = key * multiplier
}

func (self *SimpleCtx) GetValue(key int) int {
	if self.ints == nil {
		return -1
	}

	if val, found := self.ints[key]; found {
		return val
	}

	return -1
}

func (self *SimpleCtx) IntExists(key int) bool {
	if self.ints == nil {
		return false
	}
	if _, ok := self.ints[key]; ok {
		return true
	}

	return false
}

func (self *SimpleCtx) DelInt(key int) {
	if self.ints != nil {
		delete(self.ints, key)
	}
}

func (self *SimpleCtx) DeleteUser(username string) {
	if self.users == nil {
		return
	}
	delete(self.users, username)
}

func (self *SimpleCtx) Put(username string, value int) {
	if self.users == nil {
		self.users = make(map[string]*User)
	}
	self.users[username] = &User{
		Login:  username,
		Number: value,
	}

}

type AppCtx interface {
	UserExists(username string) bool
	DeleteUser(username string)
	Put(username string, value int)
	SetInt(key, multiplier int)
	DelInt(key int)
	IntExists(key int) bool
	Logf(format string, args ...any)
}

func TestBus_IDGenError(t *testing.T) {
	idGenErr := errors.New("id generator failure")
	bus := NewDefault(func(AppCtx) (ID8Byte, error) {
		return ID8Byte{}, idGenErr
	})
	ctx := &SimpleCtx{}
	err := bus.Publish(ctx, []Payload[AppCtx]{
		&UserData{Login: "xyz"},
	})
	assert.Equal(t, idGenErr, err)
}

func TestBus_EmptyPayloads(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[AppCtx]())
	ctx := &SimpleCtx{}

	err := bus.Publish(ctx, []Payload[AppCtx]{})
	assert.Equal(t, ErrEmptyPayloads, err)
}

func TestBus_RollbackOnHandleError(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[AppCtx]())
	ctx := &SimpleCtx{
		logf: t.Logf,
	}
	err := bus.Publish(ctx, []Payload[AppCtx]{
		&UserData{Login: "test", age: 150}, // Handle will return "not possible"
	})
	assert.EqualError(t, err, "not possible")
	assert.False(t, ctx.UserExists("test"))
}

func TestBus_RunInvalidCommand(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[AppCtx]())
	ctx := &SimpleCtx{}
	_, err := Run[AppCtx, ID8Byte, int](ctx, bus, []Payload[AppCtx]{
		&UserData{Login: "test"},
	})
	assert.Equal(t, ErrPayloadIsNotCommand, err)
}

func TestBus_Subscribe(t *testing.T) {

	subs := NewSubscribers[AppCtx, ID8Byte]()

	bus := NewDefault(NewID8ByteHandler[AppCtx](), subs.Notifier(),
		func(next EventHandler[AppCtx, ID8Byte]) EventHandler[AppCtx, ID8Byte] {
			return func(ctx AppCtx, event *Event[AppCtx, ID8Byte]) error {
				err := next(ctx, event)
				if err == nil {
					var line string
					for _, p := range event.Payloads {
						if u, ok := p.(*UserData); ok {
							if line != "" {
								line += ", "
							}
							line += u.Login
						}
					}
					t.Logf("event commited %v %s\n", event.ID, line)

				}
				return err
			}
		})
	simpleCtx := &SimpleCtx{
		bus:  bus,
		logf: t.Logf,
	}

	//

	subs.Subscribe(&UserData{}, func(ctx AppCtx, payload Payload[AppCtx]) {
		t.Log("Created user", payload.(*UserData).Login)
	})

	assert.NoError(t, bus.Publish(simpleCtx,
		[]Payload[AppCtx]{
			&UserData{age: 100, Login: "marcin"},
			&UserData{Login: "janek"},
		}),
	)

	// tx fail
	assert.EqualError(t, bus.Publish(simpleCtx,
		[]Payload[AppCtx]{
			&UserData{Login: "hieronim"},
			&UserData{Login: "marcin"},
		}), "duplicated login marcin",
	)

	assert.False(t, simpleCtx.UserExists("hieronim"))

	// tx success
	assert.NoError(t, bus.Publish(simpleCtx,
		[]Payload[AppCtx]{
			&UserData{Login: "hieronim"},
			&UserData{Login: "grażyna"},
		}),
	)
	assert.True(t, simpleCtx.UserExists("hieronim"))
	assert.True(t, simpleCtx.UserExists("grażyna"))

	assert.EqualError(t, bus.Publish(simpleCtx,
		[]Payload[AppCtx]{
			&UserData{age: 150, Login: "Håndtering av udøde"},
		}), "not possible",
	)

	assert.False(t, simpleCtx.UserExists("Håndtering av udøde"))

	// the first event is not a command, throw error
	_, err := Run[AppCtx, ID8Byte, int](simpleCtx, bus, []Payload[AppCtx]{
		&UserData{Login: "julcia"},
		&EventWithResult{i: 1},
	})
	assert.Equal(t, ErrPayloadIsNotCommand, err)

	// An example of a correctly executed command, the result from the first event
	val := 501
	multiplier, err := Run[AppCtx, ID8Byte, int](simpleCtx, bus, []Payload[AppCtx]{
		&EventWithResult{i: val},
		&UserData{Login: "julcia"},
	})
	assert.Nil(t, err)
	assert.Equal(t, multiplier*val, simpleCtx.GetValue(val),
		"multiplier should be equal")
}

type dummyCtx struct{}

type dummyPayload struct{}

func (d *dummyPayload) Validate(ctx dummyCtx) error { return nil }
func (d *dummyPayload) Commit(ctx dummyCtx)         {}
func (d *dummyPayload) Rollback(ctx dummyCtx)       {}
func (d *dummyPayload) PayloadType() PayloadType    { return 0 }
func (d *dummyPayload) Handle(ctx dummyCtx) error   { return nil }

// TestRetryMiddleware_SucceedsAfterRetries checks that the handler passes after several
// failed attempts.
func TestRetryMiddleware_SucceedsAfterRetries(t *testing.T) {
	attempts := 0
	handler := func(ctx dummyCtx, event *Event[dummyCtx, int]) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary failure")
		}
		return nil
	}

	event := &Event[dummyCtx, int]{
		ID:       1,
		Payloads: []Payload[dummyCtx]{&dummyPayload{}},
	}

	mw := Retry[dummyCtx, int](5, 10*time.Millisecond)
	err := mw(handler)(dummyCtx{}, event)
	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

// TestRetryMiddleware_FailsAfterMaxRetries It checks that when the limit is reached,
// the middleware returns an error.
func TestRetryMiddleware_FailsAfterMaxRetries(t *testing.T) {
	attempts := 0
	handler := func(ctx dummyCtx, event *Event[dummyCtx, int]) error {
		attempts++
		return errors.New("always fails")
	}

	event := &Event[dummyCtx, int]{
		ID:       2,
		Payloads: []Payload[dummyCtx]{&dummyPayload{}},
	}

	mw := Retry[dummyCtx, int](3, 5*time.Millisecond)
	err := mw(handler)(dummyCtx{}, event)
	assert.Error(t, err)
	assert.Equal(t, 3, attempts)
}

type testCtx struct{}

type testPayload struct {
	payloadType PayloadType
}

func (p *testPayload) Validate(testCtx) error   { return nil }
func (p *testPayload) Commit(testCtx)           {}
func (p *testPayload) Handle(testCtx) error     { return nil }
func (p *testPayload) Rollback(testCtx)         {}
func (p *testPayload) PayloadType() PayloadType { return p.payloadType }

func TestLoggingById(t *testing.T) {
	var logs []string
	logger := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Logf("add line: %s\n", line)
		logs = append(logs, line)
	}

	event := &Event[testCtx, int]{
		ID:       123,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 1}},
	}

	logged := LoggingById[testCtx, int](logger)(func(ctx testCtx, e *Event[testCtx, int]) error {
		return nil
	})

	err := logged(testCtx{}, event)
	assert.NoError(t, err)
	assert.Condition(t, func() bool {
		for _, l := range logs {
			if strings.Contains(l, "Payload type: 1") {
				return true
			}
		}
		return false
	}, "should log numeric payload type")
}

func TestLoggingByTypeName(t *testing.T) {
	var logs []string
	logger := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Logf("add line: %s\n", line)
		logs = append(logs, line)
	}

	nameMap := map[PayloadType]string{
		1: "CustomType",
	}

	nameHandler := func(pt PayloadType) string {
		if n, ok := nameMap[pt]; ok {
			return n
		}
		return "Unknown"
	}

	event := &Event[testCtx, int]{
		ID:       456,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 1}},
	}

	logged := LoggingByTypeName[testCtx, int](nameHandler, logger)(func(ctx testCtx, e *Event[testCtx, int]) error {
		return nil
	})

	err := logged(testCtx{}, event)
	assert.NoError(t, err)
	assert.Condition(t, func() bool {
		for _, l := range logs {
			if strings.Contains(l, "Payload type: CustomType") {
				return true
			}
		}
		return false
	}, "should log named payload type")
}

type testRetryPolicy struct {
	max   int
	delay time.Duration
}

func (r *testRetryPolicy) ShouldRetry(attempt int, err error) bool {
	return attempt < r.max-1
}

func (r *testRetryPolicy) Backoff(attempt int) time.Duration {
	return r.delay * time.Duration(attempt)
}

func TestRetryWithFallback_CallsFallbackOnFailure(t *testing.T) {
	failErr := errors.New("fail")
	calls := 0
	fallbackCalled := false
	maxCalls := 4
	policy := &testRetryPolicy{max: maxCalls, delay: 5 * time.Millisecond}

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		calls++
		t.Logf("handler call no %d\n", calls)
		return failErr
	}

	fallback := func(evt *Event[testCtx, int], err error) {
		fallbackCalled = true
		t.Logf("fallback called, calls %d\n", calls)
		assert.Equal(t, failErr, err)
		assert.Equal(t, 99, evt.ID)
	}

	evt := &Event[testCtx, int]{
		ID:       99,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 7}},
	}

	mw := RetryWithFallback[testCtx, int](policy, fallback)(handler)
	err := mw(testCtx{}, evt)

	assert.Equal(t, failErr, err)
	assert.Equal(t, maxCalls, calls)
	assert.True(t, fallbackCalled)
}

func TestRetryWithFallback_SkipsFallbackOnSuccess(t *testing.T) {
	called := 0
	maxCalls := 3
	policy := &testRetryPolicy{max: maxCalls, delay: 5 * time.Millisecond}
	fallbackCalled := false

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		called++
		t.Logf("handler call no  %d\n", called)
		return nil
	}

	fallback := func(evt *Event[testCtx, int], err error) {
		// This cannot happen
		fallbackCalled = true
		t.Logf("fallback called, calls %d\n", called)
	}

	evt := &Event[testCtx, int]{
		ID:       10,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 5}},
	}

	mw := RetryWithFallback[testCtx, int](policy, fallback)(handler)
	err := mw(testCtx{}, evt)

	fmt.Println(called)
	assert.NoError(t, err)
	assert.Equal(t, 1, called)
	assert.False(t, fallbackCalled)
}

type countedRetryPolicy struct {
	max   int
	delay time.Duration
	log   []int // keeps track of attempt indexes
}

func (r *countedRetryPolicy) ShouldRetry(attempt int, err error) bool {
	r.log = append(r.log, attempt)
	return attempt < r.max-1
}

func (r *countedRetryPolicy) Backoff(attempt int) time.Duration {
	return r.delay
}

func TestRetryWithPolicy_SucceedsAfterRetries(t *testing.T) {
	failErr := errors.New("fail")
	calls := 0
	maxCalls := 3
	policy := &countedRetryPolicy{max: maxCalls, delay: 10 * time.Millisecond}

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		calls++
		t.Logf("handler call no  %d\n", calls)
		if calls < 3 {
			return failErr
		}
		return nil
	}

	evt := &Event[testCtx, int]{
		ID:       11,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 8}},
	}

	mw := RetryWithPolicy[testCtx, int](policy)(handler)
	err := mw(testCtx{}, evt)

	assert.NoError(t, err)
	assert.Equal(t, maxCalls, calls)
	assert.Equal(t, []int{0, 1}, policy.log)
}

func TestRetryWithPolicy_GivesUpAfterMaxRetries(t *testing.T) {
	failErr := errors.New("fail again")
	maxCalls := 3
	calls := 0
	policy := &countedRetryPolicy{max: maxCalls, delay: 10 * time.Millisecond}

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		calls++
		t.Logf("handler call no  %d\n", calls)

		return failErr
	}

	evt := &Event[testCtx, int]{
		ID:       12,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 9}},
	}

	mw := RetryWithPolicy[testCtx, int](policy)(handler)
	err := mw(testCtx{}, evt)

	assert.Equal(t, failErr, err)
	assert.Equal(t, maxCalls, calls) // initial try + 2 retries
	assert.Equal(t, []int{0, 1, 2}, policy.log)
}

func TestWithEventHooks_BothHooksAreCalled(t *testing.T) {
	var beforeCalled, afterCalled bool

	hooks := EventHooks[testCtx, int]{
		OnBeforeHandle: func(ctx testCtx, evt *Event[testCtx, int]) {
			t.Logf("before handle called %d\n", evt.ID)
			beforeCalled = true
		},
		OnAfterCommit: func(ctx testCtx, evt *Event[testCtx, int]) {
			t.Logf("after commit called %d\n", evt.ID)
			afterCalled = true
		},
	}

	evt := &Event[testCtx, int]{
		ID:       21,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 1}},
	}

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		t.Logf("handler called %d\n", evt.ID)
		return nil
	}

	mw := WithEventHooks(hooks)(handler)
	err := mw(testCtx{}, evt)

	assert.NoError(t, err)
	assert.True(t, beforeCalled)
	assert.True(t, afterCalled)
}

func TestWithEventHooks_OnlyBeforeCalledOnFailure(t *testing.T) {
	var beforeCalled, afterCalled bool

	hooks := EventHooks[testCtx, int]{
		OnBeforeHandle: func(ctx testCtx, evt *Event[testCtx, int]) {
			t.Logf("before handle called %d\n", evt.ID)
			beforeCalled = true
		},
		OnAfterCommit: func(ctx testCtx, evt *Event[testCtx, int]) {
			t.Logf("after commit called %d\n", evt.ID)
			afterCalled = true
		},
	}

	evt := &Event[testCtx, int]{
		ID:       22,
		Payloads: []Payload[testCtx]{&testPayload{payloadType: 2}},
	}

	handler := func(ctx testCtx, evt *Event[testCtx, int]) error {
		return errors.New("fail")
	}

	mw := WithEventHooks(hooks)(handler)
	err := mw(testCtx{}, evt)

	assert.Error(t, err)
	assert.True(t, beforeCalled)
	assert.False(t, afterCalled)
}
