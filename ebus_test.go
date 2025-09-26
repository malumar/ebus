package ebus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	UserCreatePayload PayloadType = iota
	EventWithResultPayload
)

type EventWithResult struct {
	i         int
	randValue int
}

func (self *EventWithResult) Result() int {
	return self.randValue
}

func (self *EventWithResult) Validate(ctx Env) error {
	if self.i == 0 {
		return errors.New("invalid i value")
	}
	return nil
}
func (self *EventWithResult) Commit(ctx Env) {
	ctx.SetInt(self.i, self.randValue)
}

func (self *EventWithResult) Rollback(ctx Env) {
	self.randValue = 0
}

func (self *EventWithResult) PayloadType() PayloadType {
	return EventWithResultPayload
}

func (self *EventWithResult) Handle(ctx Env) error {
	self.randValue = rand.Intn(10)

	if ctx.IntExists(self.i * self.randValue) {
		return errors.New("duplicated int")
	}
	return nil
}

type UserCreate struct {
	RawPayload[ID8Byte]
	Login    string
	age      int
	reserved bool
}

func (self *UserCreate) Validate(ctx Env) error {
	if self.Login == "" {
		return fmt.Errorf("missing login")
	}

	if ctx.UserExists(self.Login) {
		return fmt.Errorf("duplicated login " + self.Login)
	}

	return nil
}
func (self *UserCreate) Commit(ctx Env) {
	ctx.Put(self.Login, self.age)
}

func (self *UserCreate) Rollback(ctx Env) {

	if self.reserved {
		ctx.Logf("rolling back, delete user %s, exists = %v\n", self.Login, ctx.UserExists(self.Login))
		ctx.DeleteUser(self.Login)
	}
}

func (self *UserCreate) PayloadType() PayloadType {
	return UserCreatePayload
}

func (self *UserCreate) Handle(ctx Env) error {
	// let's temporarily reserve a login for the time of commit
	// Therefore, when undoing an operation, the system will know if it can safely delete
	// can be used, for example, for chain payloads that can refer to the previous TX
	// or you can also have e.g. shadow db in Env in which you store data until the commit
	// and WithTx you finally merge databases or copy
	self.reserved = self.age > 130

	if self.reserved {
		// We do a test, save the login in this case to the database, but we return an error
		// The system will not add the record, because due to the error it will perform
		// a rollback of the following operation
		ctx.Put(self.Login, self.age)
		return errors.New("not possible")
	}

	return nil
}

func NewSimpleEnv(logf func(format string, args ...any)) *SimpleEnv {
	return &SimpleEnv{
		logf: logf,
	}
}

type record struct {
	PayloadType PayloadType
	Data        []byte
}
type SimpleTx struct {
	env  *SimpleEnv
	data []record
}

func (s *SimpleTx) Commit() error {
	if len(s.data) == 0 {
		return errors.New("tx have no data")
	}
	s.env.Logf("Updater: saving to db")
	for k, v := range s.data {
		s.env.Logf("\t %d, %v %s", k+1, v.PayloadType, string(v.Data))
	}

	return nil
}

func (s *SimpleTx) Rollback() error {
	return nil
}
func (s *SimpleTx) PutRaw(raw *Raw[ID8Byte]) error {
	s.data = append(s.data, record{raw.Type, Clone(raw.Body)})
	return nil
}

type SimpleEnv struct {
	users map[string]*User
	ints  map[int]int
	bus   *Bus[Env, ID8Byte]
	logf  func(format string, args ...any)
}

func (self *SimpleEnv) BeginTx(readonly bool) (Tx, error) {
	if self.logf == nil {
		return nil, errors.New("SimpleEnv must have assigned logf")
	}
	return &SimpleTx{
		env: self,
	}, nil
}

func (self *SimpleEnv) Rollback() {
	//TODO implement me
	panic("implement me")
}

type User struct {
	Login  string
	Number int
}

// fixme po co ta funkcja?? to jest duplikacja stagera?
func (self *SimpleEnv) PutRawX(payloadType PayloadType, raw []byte) error {
	self.Logf("db insert raw payload %v --> %v\n", payloadType, string(raw))
	// simulate disk error

	return nil
}

func (self *SimpleEnv) Updater(handler func(stager Tx) error) error {
	ss := SimpleTx{
		data: []record{},
	}
	if err := handler(&ss); err != nil {
		self.Logf("Updater: can't save data into db %v", err)
		return err
	} else {
		self.Logf("Updater1: saving to db")
		for k, v := range ss.data {
			self.Logf("\t %d, %v %s", k+1, v.PayloadType, string(v.Data))
		}
	}
	return nil
}
func (self *SimpleEnv) Logf(format string, args ...any) {
	self.logf(format, args...)
}
func (self *SimpleEnv) UserExists(username string) bool {
	if self.users == nil {
		return false
	}
	if _, ok := self.users[username]; ok {
		return true
	}

	return false
}

func (self *SimpleEnv) SetInt(key, multiplier int) {
	if self.ints == nil {
		self.ints = make(map[int]int)
	}

	self.ints[key] = key * multiplier
}

func (self *SimpleEnv) GetValue(key int) int {
	if self.ints == nil {
		return -1
	}

	if val, found := self.ints[key]; found {
		return val
	}

	return -1
}

func (self *SimpleEnv) IntExists(key int) bool {
	if self.ints == nil {
		return false
	}
	if _, ok := self.ints[key]; ok {
		return true
	}

	return false
}

func (self *SimpleEnv) DelInt(key int) {
	if self.ints != nil {
		delete(self.ints, key)
	}
}

func (self *SimpleEnv) DeleteUser(username string) {
	if self.users == nil {
		return
	}
	delete(self.users, username)
}

func (self *SimpleEnv) Put(username string, value int) {
	if self.users == nil {
		self.users = make(map[string]*User)
	}
	self.users[username] = &User{
		Login:  username,
		Number: value,
	}

}

type Env interface {
	UserExists(username string) bool
	DeleteUser(username string)
	Put(username string, value int)
	//PutRaw(payloadType PayloadType, raw []byte) error
	SetInt(key, multiplier int)
	DelInt(key int)
	IntExists(key int) bool
	Logf(format string, args ...any)
	Transactional
}

func TestBus_IDGenError(t *testing.T) {
	idGenErr := errors.New("id generator failure")
	bus := NewDefault(func(Env) (ID8Byte, error) {
		return ID8Byte{}, idGenErr
	})
	env := NewSimpleEnv(t.Logf)
	err := bus.Publish(env,
		&UserCreate{Login: "xyz"},
	)
	assert.Equal(t, idGenErr, err)
}

func TestBus_RollbackOnHandleError(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[Env](0))
	env := NewSimpleEnv(t.Logf)
	err := bus.Publish(env,
		&UserCreate{Login: "test", age: 150}, // Handle will return "not possible"
	)
	assert.EqualError(t, err, "not possible")
	assert.False(t, env.UserExists("test"))
}

func TestBus_RunInvalidCommand(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[Env](0))
	env := NewSimpleEnv(t.Logf)
	_, err := Run[Env, ID8Byte, int](env, bus,
		&UserCreate{Login: "test"},
	)
	assert.Equal(t, ErrPayloadIsNotCommand, err)
}

func TestBus_Subscribe(t *testing.T) {

	subs := NewSubscribers[Env, ID8Byte]()

	bus := NewDefault(NewID8ByteHandler[Env](0), subs.Notifier(),
		func(next EventHandler[Env, ID8Byte]) EventHandler[Env, ID8Byte] {
			return func(ctx Env, event *Event[Env, ID8Byte]) error {
				err := next(ctx, event)
				if err == nil {
					var line string
					for _, p := range event.Payloads {
						if u, ok := p.(*UserCreate); ok {
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
	simpleEnv := &SimpleEnv{
		bus:  bus,
		logf: t.Logf,
	}

	//

	subs.Subscribe(&UserCreate{}, func(ctx Env, payload Payload[Env]) {
		t.Log("Created user", payload.(*UserCreate).Login)
	})

	assert.NoError(t, bus.PublishAll(simpleEnv,
		[]Payload[Env]{
			&UserCreate{age: 100, Login: "marcin"},
			&UserCreate{Login: "janek"},
		}),
	)

	// tx fail
	assert.EqualError(t, bus.PublishAll(simpleEnv,
		[]Payload[Env]{
			&UserCreate{Login: "hieronim"},
			&UserCreate{Login: "marcin"},
		}), "duplicated login marcin",
	)

	assert.False(t, simpleEnv.UserExists("hieronim"))

	// tx success
	assert.NoError(t, bus.PublishAll(simpleEnv,
		[]Payload[Env]{
			&UserCreate{Login: "hieronim"},
			&UserCreate{Login: "grażyna"},
		}),
	)
	assert.True(t, simpleEnv.UserExists("hieronim"))
	assert.True(t, simpleEnv.UserExists("grażyna"))

	assert.EqualError(t, bus.Publish(simpleEnv,
		&UserCreate{age: 150, Login: "Håndtering av udøde"},
	), "not possible",
	)

	assert.False(t, simpleEnv.UserExists("Håndtering av udøde"))

	// the first event is not a command, throw error
	_, err := RunAll[Env, ID8Byte, int](simpleEnv, bus, []Payload[Env]{
		&UserCreate{Login: "julcia"},
		&EventWithResult{i: 1},
	})
	assert.Equal(t, ErrPayloadIsNotCommand, err)

	// An example of a correctly executed command, the result from the first event
	val := 501
	multiplier, err := RunAll[Env, ID8Byte, int](simpleEnv, bus, []Payload[Env]{
		&EventWithResult{i: val},
		&UserCreate{Login: "julcia"},
	})
	assert.Nil(t, err)
	assert.Equal(t, multiplier*val, simpleEnv.GetValue(val),
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

	logged := LoggingByID[testCtx, int](logger)(func(ctx testCtx, e *Event[testCtx, int]) error {
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

type FSRawStager[ID any] struct {
	dir   string
	files []string // staged pliki
}

func NewFSRawStager[ID any](dir string) *FSRawStager[ID] { return &FSRawStager[ID]{dir: dir} }

func (s *FSRawStager[ID]) PutRaw(raw *Raw[ID]) error {
	name := fmt.Sprintf("%v_%v_%d.raw.pending", raw.Type, raw.Meta.EventID, raw.Meta.TimestampUnix)

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		// Is the directory already exists?
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	tmp := filepath.Join(s.dir, name)
	if err := os.WriteFile(tmp, raw.Body, 0o644); err != nil {
		return err
	}
	// fdatasync + fsync directory welcome, omitted here for brevity
	s.files = append(s.files, tmp)
	return nil
}

func (s *FSRawStager[ID]) Commit() error {
	for _, tmp := range s.files {
		final := strings.TrimSuffix(tmp, ".pending")
		if err := os.Rename(tmp, final); err != nil {
			return err
		}
	}
	s.files = nil
	return nil
}

func (s *FSRawStager[ID]) Rollback() error {
	for _, tmp := range s.files {
		_ = os.Remove(tmp)
	}
	s.files = nil
	return nil
}

type TxCtx[ID any] struct {
	AppCtx Env
	Stager *FSRawStager[ID]
}

type AEvent = Event[Env, ID8Byte]

func TestTx(t *testing.T) {
	// ID handler ebus – Twój istniejący
	dec := NewJSONDecoder[Env]()

	assert.NoError(t, dec.Register(UserCreatePayload, func() Payload[Env] {
		return &UserCreate{}
	}))
	//

	bus := NewWithTx(
		NewID8ByteHandler[Env](0),
		WithEventHooks[Env, ID8Byte](EventHooks[Env, ID8Byte]{
			OnBeforeHandle: func(ctx Env, evt *AEvent) {
				// e.g. tracinglog – you have access to the entire event and payloads
				log.Printf("START id=%x payloads=%d", evt.ID, len(evt.Payloads))

			},
			OnAfterCommit: func(ctx Env, evt *AEvent) {
				//will only be executed when everything has gone through and the transaction has been OK
				log.Printf("COMMITTED id=%x", evt.ID)
				// You can also fire up metrics, audit, etc.

			},
		}),

		// ...here are your middleware: retry, logging, subscribers, etc.
	)

	userData := UserCreate{Login: "marcin"}
	raw := ToJson(userData)

	env := NewSimpleEnv(t.Logf)
	if err := bus.PublishRaw(env, dec, userData.PayloadType(), raw); err != nil {
		log.Fatal(err)
	}

}
func TestCustomDispatchTx(t *testing.T) {
	// ebus handler ID – your existing one
	dec := NewJSONDecoder[Env]()

	assert.NoError(t, dec.Register(UserCreatePayload, func() Payload[Env] {
		return &UserCreate{}
	}))
	//

	bus := New(
		NewID8ByteHandler[Env](0),
		func(env Env, event *Event[Env, ID8Byte]) error {

			// 1) start a transaction in DB if you have one (optional)
			// tx := db.Begin()
			// ctx = ctxWithTx(ctx, tx)

			if err := ValidateAll(env, event); err != nil {
				return err
			}

			// 2) stager for the time of dispatch
			st := NewFSRawStager[ID8Byte]("/tmp/wal-stage-test")
			//tx := TxCtx[ID8Byte]{
			//	Env: ctx,
			//	Tx: st,
			//}

			nHandled, err := ApplyAllUnsafe(env, event, st)
			if err != nil {
				// Undo changes for all completed + currently incorrect
				if e := st.Rollback(); e != nil {
					env.Logf("Rollback error after  ApplyAllUnsafe %v", e)
				}
				RollbackRangeUnsafe(env, event, 0, nHandled+1)
				return err
			}

			CommitAllUnsafe(env, event)

			// 4) finalization: DB first, then stager (or vice versa – choose one order
			// and stick to it)
			// if err := tx.Commit(); err != nil {
			//     _ = st.Rollback()
			//     return err
			// }

			if err := st.Commit(); err != nil {
				// optional: DB compensation, if the DB commit has already gone
				return err
			}

			return nil

		},
		WithEventHooks[Env, ID8Byte](EventHooks[Env, ID8Byte]{
			OnBeforeHandle: func(ctx Env, evt *AEvent) {
				// e.g. tracinglog – you have access to the entire event and payload
				log.Printf("START id=%x payloads=%d", evt.ID, len(evt.Payloads))

			},
			OnAfterCommit: func(ctx Env, evt *AEvent) {
				// will only be executed when everything has gone through and the transaction has been OK
				log.Printf("COMMITTED id=%x", evt.ID)
				// You can also fire up metrics, audit, etc.

			},
		}),

		// ...here are your middleware: retry, logging, subscribers, etc.
	)

	userData := UserCreate{Login: "marcin"}
	raw := ToJson(userData)

	env := NewSimpleEnv(t.Logf)
	if err := bus.PublishRaw(env, dec, userData.PayloadType(), raw); err != nil {
		log.Fatal(err)
	}

}

// ------- Rollback tests helpers -------

type recPayload struct {
	RawPayload[ID8Byte]
	id        string
	pt        PayloadType
	events    *[]string
	handleErr error
}

func (p *recPayload) Validate(Env) error       { return nil }
func (p *recPayload) PayloadType() PayloadType { return p.pt }
func (p *recPayload) Handle(Env) error {
	if p.events != nil {
		*p.events = append(*p.events, "handle:"+p.id)
	}
	return p.handleErr
}
func (p *recPayload) Commit(Env) {
	if p.events != nil {
		*p.events = append(*p.events, "commit:"+p.id)
	}
}
func (p *recPayload) Rollback(Env) {
	if p.events != nil {
		*p.events = append(*p.events, "rollback:"+p.id)
	}
}

// Env-wrapper to inject your own Tx
type txEnv struct {
	*SimpleEnv
	mkTx func() Tx
}

func (e txEnv) BeginTx(readonly bool) (Tx, error) { return e.mkTx(), nil }

// TX, which always fails on PutRaw (staging)
type stagingFailTx struct {
	rolledBack bool
}

func (s *stagingFailTx) Commit() error                { return nil }
func (s *stagingFailTx) Rollback() error              { s.rolledBack = true; return nil }
func (s *stagingFailTx) PutRaw(_ *Raw[ID8Byte]) error { return errors.New("staging failed") }

// TX, which fails on the Commit and saves whether the Rollback was called
type commitFailTx struct {
	rolledBack bool
	staged     int
}

func (c *commitFailTx) Commit() error                { return errors.New("commit failed") }
func (c *commitFailTx) Rollback() error              { c.rolledBack = true; return nil }
func (c *commitFailTx) PutRaw(_ *Raw[ID8Byte]) error { c.staged++; return nil }

// Rollback of the order on the Handle error (Dispatcher)
func TestDispatcher_RollbackOrder_OnHandleError(t *testing.T) {
	var evs []string
	bus := NewDefault(NewID8ByteHandler[Env](0))
	env := NewSimpleEnv(t.Logf)

	a := &recPayload{id: "a", pt: 100, events: &evs}
	b := &recPayload{id: "b", pt: 101, events: &evs}
	c := &recPayload{id: "c", pt: 102, events: &evs, handleErr: errors.New("boom")}

	err := bus.PublishAll(env, []Payload[Env]{a, b, c})
	assert.EqualError(t, err, "boom")

	// Handle: a, b; błąd na c -> rollback: c, b, a (odwrotna kolejność)
	assert.Equal(t, []string{
		"handle:a", "handle:b", "handle:c",
		"rollback:c", "rollback:b", "rollback:a",
	}, evs, "rollback should happen in reverse order and include failing payload")
}

// Rollback on PutRaw staging error (TxDispatcher) – before Handle
func TestTx_Rollback_OnStagingError(t *testing.T) {
	var evs []string

	// Bus z TxDispatcher
	bus := NewWithTx(NewID8ByteHandler[Env](0))

	// Env, który zwraca stagingFailTx (PutRaw -> error)
	env := txEnv{
		SimpleEnv: NewSimpleEnv(t.Logf),
		mkTx:      func() Tx { return &stagingFailTx{} },
	}

	// Decoder zwracający recPayload implementujący RawKeeper
	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(200, func() Payload[Env] {
		return &recPayload{id: "p0", pt: 200, events: &evs}
	}))

	// Minimalne RAW body
	body := []byte(`{}`)

	err := bus.PublishRaw(env, dec, 200, body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stage raw", "should propagate staging error")

	// Since staging fails before Tx, our pipeline also calls a rollback for the first
	// payload (+1), so we only expect a rollback (without a handlecommit).
	assert.Equal(t, []string{
		"rollback:p0",
	}, evs, "failing payload should be rolled back even if Handle did not run (consistent +1 semantics)")
}

// Rollback on Commit() TX error – after successful Handle
func TestTx_Rollback_OnCommitFailure(t *testing.T) {
	var evs []string

	cftx := &commitFailTx{}
	env := txEnv{
		SimpleEnv: NewSimpleEnv(t.Logf),
		mkTx:      func() Tx { return cftx },
	}

	bus := NewWithTx(NewID8ByteHandler[Env](0))

	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(201, func() Payload[Env] {
		return &recPayload{id: "a", pt: 201, events: &evs}
	}))
	assert.NoError(t, dec.Register(202, func() Payload[Env] {
		return &recPayload{id: "b", pt: 202, events: &evs}
	}))

	bodyA := []byte(`{}`)
	bodyB := []byte(`{}`)

	// We are publishing 2 payloads in one event – both will enter Tx,
	// staging will pass; Commit TX will fail
	err := bus.PublishRaws(env, dec,
		Raw[ID8Byte]{Type: 201, Body: bodyA},
		Raw[ID8Byte]{Type: 202, Body: bodyB},
	)
	assert.EqualError(t, err, "commit failed")

	// We are publishing 2 payloads in one event – both will enter
	// Tx, staging will pass; Commit TX will fail
	assert.Equal(t, []string{
		"handle:a", "handle:b",
		"rollback:b", "rollback:a",
	}, evs, "should rollback all handled payloads in reverse order on commit failure")

	// In addition: we check that tx. Rollback was triggered after a failed Commit
	assert.True(t, cftx.rolledBack, "tx.Rollback should be called after commit failure")
}

// --- Validate fail: brak stagingu, brak TX, brak handlerollback ---
// Validate: No Handle, No Staging, No TX on Validation Error
// Dispatcher: should not call Handle/Commit/Rollback.
// TxDispatcher: Validate fail before BeginTx – BeginTx should not be invoked, PutRaw should not be invoked.
type validateFailPayload struct {
	calls *[]string
}

func (p *validateFailPayload) Validate(Env) error { return errors.New("validate fail") }
func (p *validateFailPayload) Handle(Env) error {
	if p.calls != nil {
		*p.calls = append(*p.calls, "handle")
	}
	return nil
}
func (p *validateFailPayload) Commit(Env) {
	if p.calls != nil {
		*p.calls = append(*p.calls, "commit")
	}
}
func (p *validateFailPayload) Rollback(Env) {
	if p.calls != nil {
		*p.calls = append(*p.calls, "rollback")
	}
}
func (p *validateFailPayload) PayloadType() PayloadType { return 300 }

func TestValidateFail_NoHandleNoStaging_Dispatcher(t *testing.T) {
	var calls []string
	bus := NewDefault(NewID8ByteHandler[Env](0))
	env := NewSimpleEnv(t.Logf)

	err := bus.Publish(env, &validateFailPayload{calls: &calls})
	assert.EqualError(t, err, "validate fail")
	assert.Equal(t, 0, len(calls), "no handle/commit/rollback on validate fail")
}

// Tx call detection
type countingTx struct {
	beginCalled bool
	putRawCount int
	rolledBack  bool
	committed   bool
}

func (tx *countingTx) Commit() error                { tx.committed = true; return nil }
func (tx *countingTx) Rollback() error              { tx.rolledBack = true; return nil }
func (tx *countingTx) PutRaw(_ *Raw[ID8Byte]) error { tx.putRawCount++; return nil }

func TestValidateFail_NoBeginTxNoStaging_TxDispatcher(t *testing.T) {
	var beginCounter int
	var calls []string

	tx := &countingTx{}
	env := txEnv{
		SimpleEnv: NewSimpleEnv(t.Logf),
		mkTx: func() Tx {
			beginCounter++
			return tx
		},
	}
	bus := NewWithTx(NewID8ByteHandler[Env](0))

	err := bus.Publish(env, &validateFailPayload{calls: &calls})
	assert.EqualError(t, err, "validate fail")
	assert.Equal(t, 0, len(calls))
	assert.Equal(t, 0, beginCounter, "BeginTx must not be called if Validate fails")
	assert.Equal(t, 0, tx.putRawCount, "no staging on validate fail")
	assert.False(t, tx.rolledBack)
	assert.False(t, tx.committed)
}

// Mixed payloads without RawKeeper: staging only for RawKeeper, meta set Payload without RawKeeper
type noRawPayload struct {
	id    string
	calls *[]string
	pt    PayloadType
}

func (p *noRawPayload) Validate(Env) error { return nil }
func (p *noRawPayload) Handle(Env) error {
	if p.calls != nil {
		*p.calls = append(*p.calls, "handle:"+p.id)
	}
	return nil
}
func (p *noRawPayload) Commit(Env)               {}
func (p *noRawPayload) Rollback(Env)             {}
func (p *noRawPayload) PayloadType() PayloadType { return p.pt }

// RAW Capture Stager
type capturingTx struct {
	staged []*Raw[ID8Byte]
}

func (c *capturingTx) Commit() error                { return nil }
func (c *capturingTx) Rollback() error              { return nil }
func (c *capturingTx) PutRaw(r *Raw[ID8Byte]) error { c.staged = append(c.staged, r); return nil }

func TestMixedPayloads_StagingOnlyForRawKeeper_MetaSet(t *testing.T) {
	cap := &capturingTx{}
	env := txEnv{
		SimpleEnv: NewSimpleEnv(t.Logf),
		mkTx:      func() Tx { return cap },
	}
	bus := NewWithTx(NewID8ByteHandler[Env](0))

	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(400, func() Payload[Env] { return &recPayload{id: "raw", pt: 400} }))
	assert.NoError(t, dec.Register(401, func() Payload[Env] { return &noRawPayload{id: "nraw", pt: 401} }))

	err := bus.PublishRaws(env, dec,
		Raw[ID8Byte]{Type: 400, Body: []byte(`{}`)},
		Raw[ID8Byte]{Type: 401, Body: []byte(`{}`)},
	)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(cap.staged), "only RawKeeper payload should be staged")
	st := cap.staged[0]
	assert.NotNil(t, st)
	assert.Equal(t, PayloadType(400), st.Type)
	assert.NotZero(t, st.Meta.TimestampUnix)
}

// Subscribers are not invoked on an error; are triggered by success
func TestSubscribers_CalledOnlyOnSuccess(t *testing.T) {
	var cnt int
	subs := NewSubscribers[Env, ID8Byte]()
	subs.Subscribe(&UserCreate{}, func(ctx Env, p Payload[Env]) { cnt++ })

	bus := NewDefault(NewID8ByteHandler[Env](0), subs.Notifier())
	env := NewSimpleEnv(t.Logf)

	// fail
	err := bus.PublishAll(env, []Payload[Env]{
		&UserCreate{Login: "dup", age: 200}, // Handle returns "not possible"
	})
	assert.Error(t, err)
	assert.Equal(t, 0, cnt)

	// success
	assert.NoError(t, bus.PublishAll(env, []Payload[Env]{
		&UserCreate{Login: "ok1", age: 20},
	}))
	assert.Equal(t, 1, cnt)
}

// Hooks in the transaction path: OnAfterCommit only on success
func TestHooks_InTx_AfterCommitOnlyOnSuccess(t *testing.T) {
	var before, after int
	h := WithEventHooks[Env, ID8Byte](EventHooks[Env, ID8Byte]{
		OnBeforeHandle: func(ctx Env, e *Event[Env, ID8Byte]) { before++ },
		OnAfterCommit:  func(ctx Env, e *Event[Env, ID8Byte]) { after++ },
	})
	bus := NewWithTx(NewID8ByteHandler[Env](0), h)
	env := NewSimpleEnv(t.Logf)
	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(UserCreatePayload, func() Payload[Env] { return &UserCreate{} }))

	// success
	assert.NoError(t, bus.PublishRaw(env, dec, UserCreatePayload, ToJson(UserCreate{Login: "a", age: 10})))
	// fail (ustawiamy age w Go, nie przez JSON)
	assert.Error(t, bus.Publish(env, &UserCreate{Login: "b", age: 200}))

	assert.Equal(t, 2, before)
	assert.Equal(t, 1, after)
}

// flakyCmd Retry + Tx: two attempts failed (Tx), third successful; Commit and subscribers called once
type flakyCmd struct {
	RawPayload[ID8Byte]
	pt       PayloadType
	attempts *int
	failFor  int
}

func (c *flakyCmd) Validate(Env) error       { return nil }
func (c *flakyCmd) PayloadType() PayloadType { return c.pt }
func (c *flakyCmd) Handle(Env) error {
	*c.attempts++
	if *c.attempts <= c.failFor {
		return errors.New("transient")
	}
	return nil
}
func (c *flakyCmd) Commit(Env)   {}
func (c *flakyCmd) Rollback(Env) {}
func TestRetryWithTx_SucceedsAfterRetries(t *testing.T) {
	attempts := 0
	subs := NewSubscribers[Env, ID8Byte]()
	var committed int
	subs.Subscribe(&flakyCmd{pt: 500}, func(ctx Env, p Payload[Env]) { committed++ })

	bus := NewWithTx(NewID8ByteHandler[Env](0),
		Retry[Env, ID8Byte](5, 5*time.Millisecond),
		subs.Notifier(),
	)
	env := NewSimpleEnv(t.Logf)

	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(500, func() Payload[Env] {
		return &flakyCmd{pt: 500, attempts: &attempts, failFor: 2}
	}))

	// we use RAW to enable staging and not get "tx have no data"
	assert.NoError(t, bus.PublishRaw(env, dec, 500, []byte(`{}`)))
	assert.Equal(t, 3, attempts)
	assert.Equal(t, 1, committed)
}

// JSONDecoder – unknown type i unknown field
func TestJSONDecoder_UnknownType(t *testing.T) {
	dec := NewJSONDecoder[Env]()
	_, err := dec.Decode(9999, []byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown payload type")
}

type xPayload struct {
	Foo string `json:"foo"`
}

func (x *xPayload) Validate(Env) error       { return nil }
func (x *xPayload) Handle(Env) error         { return nil }
func (x *xPayload) Commit(Env)               {}
func (x *xPayload) Rollback(Env)             {}
func (x *xPayload) PayloadType() PayloadType { return 600 }

func TestJSONDecoder_DisallowUnknownFields(t *testing.T) {
	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(600, func() Payload[Env] { return &xPayload{} }))
	_, err := dec.Decode(600, []byte(`{"foo":"bar","unknown":true}`))
	assert.Error(t, err, "unknown field should cause error due to DisallowUnknownFields")
}

// panicPayload Recovery – Dispatcher (middleware) and TxDispatcher (built-in recover)
type panicPayload struct{}

func (p *panicPayload) Validate(Env) error       { return nil }
func (p *panicPayload) Handle(Env) error         { panic("boom") }
func (p *panicPayload) Commit(Env)               {}
func (p *panicPayload) Rollback(Env)             {}
func (p *panicPayload) PayloadType() PayloadType { return 700 }

func TestRecoveryMiddleware_Dispatcher_PanicToError(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[Env](0), Recovery[Env, ID8Byte](t.Logf))
	env := NewSimpleEnv(t.Logf)
	err := bus.Publish(env, &panicPayload{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic:")
}

// rbTx TX that marks the rollback
type rbTx struct{ rolledBack, did bool }

func (r *rbTx) Commit() error                { return nil }
func (r *rbTx) Rollback() error              { r.did = true; r.rolledBack = true; return nil }
func (r *rbTx) PutRaw(_ *Raw[ID8Byte]) error { return nil }

func TestTxDispatcher_PanicToError_WithRollback(t *testing.T) {
	// Ugly hack just for testing, tx is always the same
	rb := &rbTx{}
	env := txEnv{
		SimpleEnv: NewSimpleEnv(t.Logf),
		mkTx:      func() Tx { return rb },
	}
	bus := NewWithTx(NewID8ByteHandler[Env](0))
	err := bus.Publish(env, &panicPayload{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic:")

	assert.True(t, rb.rolledBack, "tx.rollback should be called on panic in TxDispatcher")
}

// TestDispatcher_Rollback_FirstPayloadFails Rollback limits – fail at index 0 (first)
func TestDispatcher_Rollback_FirstPayloadFails(t *testing.T) {
	var evs []string
	bus := NewDefault(NewID8ByteHandler[Env](0))
	env := NewSimpleEnv(t.Logf)

	a := &recPayload{id: "a", pt: 801, events: &evs, handleErr: errors.New("boom")}
	b := &recPayload{id: "b", pt: 802, events: &evs}

	err := bus.PublishAll(env, []Payload[Env]{a, b})
	assert.EqualError(t, err, "boom")
	assert.Equal(t, []string{
		"handle:a",
		"rollback:a",
	}, evs)
}

// Env, which implements ContextCarrier
type ctxEnv struct{ ctx context.Context }

func (e ctxEnv) Context() context.Context { return e.ctx }

type retryEnv struct{ ctx context.Context }

func (e retryEnv) Context() context.Context { return e.ctx }

// Minimal payload bound to retryEnv
type retryPayload struct{}

func (p *retryPayload) Validate(retryEnv) error  { return nil }
func (p *retryPayload) Handle(retryEnv) error    { return errors.New("transient") }
func (p *retryPayload) Commit(retryEnv)          {}
func (p *retryPayload) Rollback(retryEnv)        {}
func (p *retryPayload) PayloadType() PayloadType { return 0 }
func TestRetryWithContext_CancelStopsRetries(t *testing.T) {
	// Env type used by this test implements ContextCarrier

	attempts := 0
	handler := func(env retryEnv, e *Event[retryEnv, int]) error {
		attempts++
		return errors.New("transient")
	}

	evt := &Event[retryEnv, int]{
		ID:       1,
		Payloads: []Payload[retryEnv]{&retryPayload{}},
	}

	// Short timeout so cancel happens before all retries
	cctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	env := retryEnv{ctx: cctx}

	// delay > timeout to guarantee we hit the cancel path
	mw := RetryWithContext[retryEnv, int](10, 50*time.Millisecond)
	err := mw(handler)(env, evt)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "canceled:")
	assert.Less(t, attempts, 10, "retries should stop on context cancel/timeout")
}

// Test: Subscribers in the Tx path – calls only after commit
func TestSubscribers_Tx_CalledOnlyOnSuccess(t *testing.T) {
	subs := NewSubscribers[Env, ID8Byte]()
	cnt := 0
	subs.Subscribe(&UserCreate{}, func(ctx Env, p Payload[Env]) { cnt++ })

	bus := NewWithTx(NewID8ByteHandler[Env](0), subs.Notifier())
	env := NewSimpleEnv(t.Logf)
	dec := NewJSONDecoder[Env]()
	assert.NoError(t, dec.Register(UserCreatePayload, func() Payload[Env] { return &UserCreate{} }))

	// success
	assert.NoError(t, bus.PublishRaw(env, dec, UserCreatePayload, ToJson(UserCreate{Login: "ok", age: 10})))
	assert.Equal(t, 1, cnt)

	// fail (Handle zwróci błąd, commit się nie wykona)
	err := bus.Publish(env, &UserCreate{Login: "dup", age: 200})
	assert.Error(t, err)
	assert.Equal(t, 1, cnt, "no new notifications on failure")
}

// PublishRaws – edge cases: Blank input and decoding error

func TestPublishRaws_Empty_NoOp(t *testing.T) {
	bus := NewDefault(NewID8ByteHandler[testCtx](0))
	env := testCtx{}
	dec := NewJSONDecoder[testCtx]()
	err := bus.PublishRaws(env, dec /* no raws */)
	assert.NoError(t, err)
}

func TestPublishRaws_DecodeError_NoProcessing(t *testing.T) {
	// decoder does not know type 42 -> decode will return an error
	bus := NewDefault(NewID8ByteHandler[testCtx](0))
	env := testCtx{}
	dec := NewJSONDecoder[testCtx]()

	// payload that would fail in Tx, but we shouldn't get to Handle
	raws := []Raw[ID8Byte]{{Type: 42, Body: []byte(`{malformed`)}}

	err := bus.PublishRaws(env, dec, raws...)
	assert.Error(t, err)
	// no side-effects for assertion (in our case testCtx is empty),
	// but the lack of panic and error alone are enough
}

// Test: panic-rollback with recPayload – rollback precision
type panicRecPayload struct {
	recPayload
	panicOnHandle bool
}

func (p *panicRecPayload) Handle(Env) error {
	if p.events != nil {
		*p.events = append(*p.events, "handle:"+p.id)
	}
	if p.panicOnHandle {
		panic("boom")
	}
	return nil
}

func TestTx_PanicRollback_OrderAndScope(t *testing.T) {
	var evs []string
	env := txEnv{SimpleEnv: NewSimpleEnv(t.Logf), mkTx: func() Tx { return &commitFailTx{} }}
	bus := NewWithTx(NewID8ByteHandler[Env](0))

	a := &panicRecPayload{recPayload: recPayload{id: "a", pt: 901, events: &evs}}
	b := &panicRecPayload{recPayload: recPayload{id: "b", pt: 902, events: &evs}, panicOnHandle: true}
	err := bus.PublishAll(env, []Payload[Env]{a, b})

	assert.Error(t, err)
	// oczekujemy: handle a, handle b (panika), rollback b, rollback a
	assert.Equal(t, []string{"handle:a", "handle:b", "rollback:b", "rollback:a"}, evs)
}

func ToJson(obj interface{}) []byte {
	dat, err := json.Marshal(obj)
	if err != nil {
		return []byte(err.Error())
	}
	return dat
}
