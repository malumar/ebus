package ebus

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"testing"
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
