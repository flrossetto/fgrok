// Package mediator implements a message routing system for fgrok.
//
// Provides functionality for:
// - Registering message handlers
// - Routing messages between components
// - Managing message correlation
package mediator

import (
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"google.golang.org/protobuf/proto"
)

//nolint:gochecknoglobals
var (
	stringType = reflect.TypeOf("")
	errorType  = reflect.TypeOf((*error)(nil)).Elem()
)

// handlerEntry represents a registered handler in the mediator
//
// Fields:
// - fn: Handler function (reflect.Value) to be executed for matching messages
// - argType: Expected argument type (pre-computed for optimization)
// - closed: Atomic flag indicating if handler was unregistered
// - wg: WaitGroup to synchronize in-flight calls during unregistration
//
// Safety:
// - closed (atomic.Bool) prevents race conditions during unregister
// - wg ensures in-flight calls complete before handler removal
type handlerEntry struct {
	fn      reflect.Value
	argType reflect.Type
	closed  atomic.Bool
	wg      sync.WaitGroup
}

// Mediator defines the mediator pattern for component communication
//
// Responsibilities:
// - Type-based message routing
// - Decoupling between senders and receivers
// - Safe concurrent processing
//
// Methods:
// - Send: Synchronous delivery with error handling
// - AsyncSend: Asynchronous delivery via channel
// - RegisterHandler: Handler registration with type validation
// - RegisterUniqueHandler: Exclusive handler registration with conflict detection
//
// Thread-safety:
// - All methods are safe for concurrent calls
type Mediator interface {
	// Send delivers a message synchronously to registered handlers
	Send(correlationID string, message any) error

	// AsyncSend delivers a message asynchronously (non-blocking)
	AsyncSend(correlationID string, message any) <-chan error

	// RegisterHandler registers a handler function and returns an unregister function
	RegisterHandler(fn any) (func(), error)

	// RegisterUniqueHandler registers a handler function if no other handler exists for the message type
	RegisterUniqueHandler(fn any) (func(), error)
}

// DefaultMediator is the concrete implementation of Mediator
//
// Structure:
// - mu: RWMutex protecting concurrent access to handlers map
// - handlerCount: Atomic counter for unique IDs
// - handlers: Hierarchical map [messageType][ID]handlerEntry
//
// Design:
// - Two-level mapping for efficient routing
// - Entry pointers enable safe unregistration
// - Optimized for concurrent reads (RLock)
type DefaultMediator struct {
	mu           sync.RWMutex
	handlerCount uint64
	handlers     map[string]map[uint64]*handlerEntry
	// In a real implementation you might want to track registration locations:
	// handlerLocations map[uint64]string // would store file:line of registration
}

// New creates a new thread-safe Mediator instance
//
// Returns:
// - Ready-to-use Mediator implementation
//
// Notes:
// - Returned instance is safe for concurrent use
// - No additional initialization needed
func New() *DefaultMediator {
	return &DefaultMediator{
		handlers: make(map[string]map[uint64]*handlerEntry),
	}
}

// RegisterUniqueHandler registers a handler function if no other handler exists for the message type
//
// Parameters:
// - fn: Handler function with signature: func(string, messageType) error
//
// Returns:
// - Function for safe unregistration
// - File and line of existing registration if found
// - Error if validation fails or handler exists
func (m *DefaultMediator) RegisterUniqueHandler(fn any) (func(), error) {
	argType := reflect.TypeOf(fn).In(1)
	key := keyFromType(argType)

	m.mu.Lock()

	// Check if handler already exists
	if len(m.handlers[key]) > 0 {
		m.mu.Unlock()

		return nil, fgrokerr.New(
			fgrokerr.CodeConflict,
			"handler already registered for this message type",
			fgrokerr.WithDetails(map[string]any{
				"message_type": key,
			}),
		)
	}

	m.mu.Unlock()

	// Delegate to RegisterHandler for the actual registration
	return m.RegisterHandler(fn)
}

// RegisterHandler registers a handler function with strict type validation
//
// Parameters:
// - fn: Handler function with signature: func(string, messageType) error
//
// Returns:
// - Function for safe unregistration
// - Error if validation fails
//
// Validations:
// - fn must be a function
// - First argument must be string (correlationID)
// - Must have exactly 2 parameters
// - May return error or nothing
func (m *DefaultMediator) RegisterHandler(fn any) (func(), error) {
	if fn == nil {
		return nil, fgrokerr.New(fgrokerr.CodeInternalError, "nil handler")
	}

	v := reflect.ValueOf(fn)
	t := v.Type()

	// Signature validation
	if t.Kind() != reflect.Func {
		return nil, fgrokerr.New(fgrokerr.CodeInternalError, "handler must be a function")
	}

	if t.NumIn() != 2 || t.In(0) != stringType {
		return nil, fgrokerr.New(fgrokerr.CodeInternalError, "invalid handler signature")
	}

	if t.NumOut() > 1 || (t.NumOut() == 1 && !t.Out(0).AssignableTo(errorType)) {
		return nil, fgrokerr.New(fgrokerr.CodeInternalError, "handler must return error or nothing")
	}

	argType := t.In(1)
	key := keyFromType(argType)

	entry := &handlerEntry{
		fn:      v,
		argType: argType,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.handlerCount++
	id := m.handlerCount

	if m.handlers[key] == nil {
		m.handlers[key] = make(map[uint64]*handlerEntry)
	}

	m.handlers[key][id] = entry

	return func() { m.unregisterHandler(key, id, entry) }, nil
}

// AsyncSend executes Send in a separate goroutine
func (m *DefaultMediator) AsyncSend(correlationID string, message any) <-chan error {
	errChan := make(chan error, 1)

	go (func() {
		defer close(errChan)
		errChan <- m.Send(correlationID, message)
	})()

	return errChan
}

// Send delivers a message to all compatible handlers
//
// Flow:
// 1. Determines routing key based on message type
// 2. Takes thread-safe snapshot of registered handlers
// 3. Executes each handler outside critical section
// 4. Returns first error encountered or nil
//
// Safety:
// - Read operation protected by RLock
// - Handlers executed safely concurrently
// - Unregistration doesn't interfere with in-flight calls
func (m *DefaultMediator) Send(correlationID string, message any) error {
	key, err := keyFromMessage(message)
	if err != nil {
		return err
	}

	m.mu.RLock()

	group := m.handlers[key]

	if len(group) == 0 {
		m.mu.RUnlock()

		return nil
	}

	// Create handlers snapshot
	snap := make([]*handlerEntry, 0, len(group))
	for _, e := range group {
		snap = append(snap, e)
	}

	m.mu.RUnlock()

	ev := reflect.ValueOf(message)
	cid := reflect.ValueOf(correlationID)

	// Process handlers
	for _, e := range snap {
		if e.closed.Load() {
			continue
		}

		e.wg.Add(1)

		arg, ok := makeArg(ev, e.argType)
		if !ok {
			e.wg.Done()

			continue
		}

		out := e.fn.Call([]reflect.Value{cid, arg})
		e.wg.Done()

		if len(out) == 1 && !out[0].IsNil() {
			if err, ok := out[0].Interface().(error); ok {
				return err
			}
		}
	}

	return nil
}

// unregisterHandler safely removes a handler
func (m *DefaultMediator) unregisterHandler(key string, id uint64, entry *handlerEntry) {
	m.mu.Lock()

	if g, ok := m.handlers[key]; ok {
		if _, exists := g[id]; exists {
			entry.closed.Store(true)
			delete(g, id)

			if len(g) == 0 {
				delete(m.handlers, key)
			}
		}
	}

	m.mu.Unlock()
	entry.wg.Wait()
}

// makeArg converts values to the expected type
func makeArg(ev reflect.Value, want reflect.Type) (reflect.Value, bool) {
	if !ev.IsValid() {
		return reflect.Value{}, false
	}

	// Try multiple conversion strategies
	if ev.Type().AssignableTo(want) {
		return ev, true
	}

	if ev.Kind() == reflect.Ptr && ev.Elem().Type().AssignableTo(want) {
		return ev.Elem(), true
	}

	if ev.CanAddr() && reflect.PointerTo(ev.Type()).AssignableTo(want) {
		return ev.Addr(), true
	}

	if ev.Type().ConvertibleTo(want) {
		return ev.Convert(want), true
	}

	return reflect.Value{}, false
}

// keyFromMessage generates routing key for messages
func keyFromMessage(v any) (string, error) {
	if v == nil {
		return "", fgrokerr.New(fgrokerr.CodeInternalError, "nil message")
	}

	if _, ok := v.(proto.Message); ok {
		return keyFromType(reflect.TypeOf(v)), nil
	}

	return "go:" + reflect.TypeOf(v).String(), nil
}

// keyFromType generates key from a type
func keyFromType(rt reflect.Type) string {
	if rt == nil {
		return "go:<nil>"
	}

	protoMsg := reflect.TypeOf((*proto.Message)(nil)).Elem()

	// Check proto.Message implementation
	if rt.Implements(protoMsg) {
		if m, _ := reflect.Zero(rt).Interface().(proto.Message); m != nil {
			return "proto:" + string(m.ProtoReflect().Descriptor().FullName())
		}

		if rt.Kind() == reflect.Ptr {
			if mm, ok := reflect.New(rt.Elem()).Interface().(proto.Message); ok {
				return "proto:" + string(mm.ProtoReflect().Descriptor().FullName())
			}
		}
	}

	if rt.Kind() != reflect.Ptr {
		ptr := reflect.PointerTo(rt)
		if ptr.Implements(protoMsg) {
			if mm, ok := reflect.New(rt).Interface().(proto.Message); ok {
				return "proto:" + string(mm.ProtoReflect().Descriptor().FullName())
			}
		}
	}

	return "go:" + rt.String()
}
