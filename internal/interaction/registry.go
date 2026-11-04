package interaction

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var (
	ErrRouteConflict      = errors.New("route already owned")
	ErrInvalidRoute       = errors.New("invalid route")
	ErrInvalidHandler     = errors.New("invalid handler")
	ErrRegistrationClosed = errors.New("registration closed")
)

// Registry owns feature routes. The zero value is usable. Seal it before
// admitting events; registration and resolution are safe for concurrent use.
type Registry struct {
	mu            sync.RWMutex
	sealed        bool
	commands      map[CommandName]Handler
	callbacks     map[CallbackPrefix]Handler
	groupMessages Handler
}

// NewRegistry returns an empty registry with no built-in commands.
func NewRegistry() *Registry { return &Registry{} }

// RegisterCommand assigns one owner to a canonical command name (without / or
// a bot suffix). Telegram syntax parsing belongs to the transport mapper.
func (r *Registry) RegisterCommand(name CommandName, handler Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sealed {
		return ErrRegistrationClosed
	}

	if !validCommandName(name) {
		return ErrInvalidRoute
	}

	if nilHandler(handler) {
		return ErrInvalidHandler
	}

	if _, exists := r.commands[name]; exists {
		return ErrRouteConflict
	}

	if r.commands == nil {
		r.commands = make(map[CommandName]Handler)
	}
	r.commands[name] = handler
	return nil
}

// RegisterCallback rejects equal and nested prefixes, regardless of owner or
// registration order, so a callback can match at most one route.
func (r *Registry) RegisterCallback(prefix CallbackPrefix, handler Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sealed {
		return ErrRegistrationClosed
	}

	if len(prefix) == 0 || len(prefix) > 64 || !utf8.ValidString(string(prefix)) {
		return ErrInvalidRoute
	}

	if nilHandler(handler) {
		return ErrInvalidHandler
	}

	for registered := range r.callbacks {
		if strings.HasPrefix(string(prefix), string(registered)) || strings.HasPrefix(string(registered), string(prefix)) {
			return ErrRouteConflict
		}
	}

	if r.callbacks == nil {
		r.callbacks = make(map[CallbackPrefix]Handler)
	}
	r.callbacks[prefix] = handler
	return nil
}

// RegisterGroupMessages assigns the sole ordinary-message subscriber. Commands
// and callbacks never fall through to this subscription.
func (r *Registry) RegisterGroupMessages(handler Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sealed {
		return ErrRegistrationClosed
	}

	if nilHandler(handler) {
		return ErrInvalidHandler
	}

	if r.groupMessages != nil {
		return ErrRouteConflict
	}

	r.groupMessages = handler
	return nil
}

// Seal closes registration before readiness. Repeated calls are harmless.
func (r *Registry) Seal() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sealed = true
}

// Resolve returns ownership without invoking handlers or interpreting feature
// content. An unsealed registry, unsupported event, or unregistered route has
// no owner; intake can skip it without executing feature behavior.
func (r *Registry) Resolve(event Event) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.sealed {
		return nil, false
	}

	// Accept pointer and value events while treating typed nils as unsupported.
	switch value := event.(type) {
	case *Command:
		if value == nil {
			return nil, false
		}

		event = *value
	case *Callback:
		if value == nil {
			return nil, false
		}

		event = *value
	case *GroupMessage:
		if value == nil {
			return nil, false
		}

		event = *value
	}

	switch value := event.(type) {
	case Command:
		if !groupIdentity(value.Identity) {
			return nil, false
		}

		handler, ok := r.commands[value.Name]
		return handler, ok
	case Callback:
		if !groupIdentity(value.Identity) {
			return nil, false
		}

		for prefix, handler := range r.callbacks {
			if strings.HasPrefix(value.Data, string(prefix)) {
				return handler, true
			}
		}
	case GroupMessage:
		if groupIdentity(value.Identity) {
			return r.groupMessages, r.groupMessages != nil
		}
	}
	return nil, false
}

func groupIdentity(identity Identity) bool {
	return identity.Chat < 0 && (identity.ChatKind == runtimecontract.ChatKindGroup || identity.ChatKind == runtimecontract.ChatKindSupergroup)
}

func validCommandName(name CommandName) bool {
	if len(name) < 1 || len(name) > 32 {
		return false
	}

	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_':
		default:
			return false
		}
	}
	return true
}

func nilHandler(handler Handler) bool {
	if handler == nil {
		return true
	}

	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
