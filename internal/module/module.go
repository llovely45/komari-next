// Package module provides the lifecycle and dependency boundary for built-in
// komari-next modules. It deliberately contains no database, HTTP or plugin
// implementation details.
package module

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Module is a trusted, in-process komari-next module.
//
// Modules are initialized and started in dependency order and stopped in the
// reverse order. External third-party plugins use a separate process protocol
// and must not implement this interface inside the server binary.
type Module interface {
	ID() string
	Dependencies() []string
	Init(context.Context, Host) error
	Start(context.Context) error
	Stop(context.Context) error
}

// ExtensionPointVersion is the version of the typed extension-point contract
// carried by Host. Later capability ports can be added behind a new version
// without putting concrete infrastructure handles in Host.
const ExtensionPointVersion = 1

// ExtensionPoint is the deliberately small, versioned boundary for future
// module capabilities. Capability ports are added in later implementation
// slices; this foundation only carries their contract version.
type ExtensionPoint struct {
	Version int
}

// Host contains only stable, dependency-injected capabilities. Concrete
// database, HTTP and plugin runtime objects are intentionally not exposed in
// this first foundation layer.
type Host struct {
	Logger     *slog.Logger
	Extensions ExtensionPoint
}

type registryState uint8

const (
	stateOpen registryState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
	stateFailed
)

// Registry owns the set of trusted in-process modules.
type Registry struct {
	mu       sync.Mutex
	host     Host
	modules  map[string]Module
	order    []string
	started  []string // started modules, or modules whose cleanup needs retry
	state    registryState
	stopDone chan struct{}
	stopErr  error
}

// NewRegistry creates an empty module registry.
func NewRegistry(host Host) *Registry {
	return &Registry{
		host:    host,
		modules: make(map[string]Module),
	}
}

// Register adds a module before the registry starts.
func (r *Registry) Register(m Module) error {
	if m == nil {
		return errors.New("module is nil")
	}
	id := strings.TrimSpace(m.ID())
	if id == "" {
		return errors.New("module id is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateOpen {
		return fmt.Errorf("module registry is not open")
	}
	if _, exists := r.modules[id]; exists {
		return fmt.Errorf("module %q is already registered", id)
	}
	r.modules[id] = m
	return nil
}

// Start initializes and starts all registered modules.
func (r *Registry) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	if r.state != stateOpen {
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("module registry cannot start in state %s", state)
	}
	r.state = stateStarting
	order, err := r.resolveOrderLocked()
	if err != nil {
		r.state = stateOpen
		r.mu.Unlock()
		return err
	}
	r.order = append([]string(nil), order...)
	modules := make(map[string]Module, len(r.modules))
	for id, m := range r.modules {
		modules[id] = m
	}
	host := r.host
	r.mu.Unlock()

	initialized := make([]string, 0, len(order))
	for _, id := range order {
		if err := modules[id].Init(ctx, host); err != nil {
			primaryErr := fmt.Errorf("initialize module %q: %w", id, err)
			pending, cleanupErr := r.stopModules(cleanupContext(ctx), modules, initialized)
			r.markFailed(pending)
			return joinLifecycleErrors(primaryErr, cleanupErr)
		}
		initialized = append(initialized, id)
	}

	started := make([]string, 0, len(order))
	for _, id := range order {
		if err := modules[id].Start(ctx); err != nil {
			primaryErr := fmt.Errorf("start module %q: %w", id, err)
			pending, cleanupErr := r.stopModules(cleanupContext(ctx), modules, initialized)
			r.markFailed(pending)
			return joinLifecycleErrors(primaryErr, cleanupErr)
		}
		started = append(started, id)
	}

	r.mu.Lock()
	r.started = append([]string(nil), started...)
	r.state = stateRunning
	r.mu.Unlock()
	return nil
}

// Stop stops started modules in reverse dependency order. It is idempotent
// when the registry was never started or has already stopped. If cleanup
// fails, a later call retries the failed modules; overlapping calls wait for
// the in-flight stop and return its final error.
func (r *Registry) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	if r.state != stateRunning && !(r.state == stateFailed && len(r.started) > 0) {
		switch r.state {
		case stateStarting:
			state := r.state
			r.mu.Unlock()
			return fmt.Errorf("module registry cannot stop in state %s", state)
		case stateStopping:
			done := r.stopDone
			r.mu.Unlock()
			select {
			case <-done:
				r.mu.Lock()
				err := r.stopErr
				r.mu.Unlock()
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			r.mu.Unlock()
			return nil
		}
	}
	modules := make(map[string]Module, len(r.modules))
	for id, m := range r.modules {
		modules[id] = m
	}
	started := append([]string(nil), r.started...)
	r.stopDone = make(chan struct{})
	r.state = stateStopping
	r.mu.Unlock()

	pending, err := r.stopModules(cleanupContext(ctx), modules, started)
	r.mu.Lock()
	r.stopErr = err
	if err == nil {
		r.state = stateStopped
		r.started = nil
	} else {
		// Keep failed cleanup modules available for an explicit retry. A
		// cleanup error means the registry is not fully stopped yet.
		r.state = stateFailed
		r.started = pending
	}
	close(r.stopDone)
	r.mu.Unlock()
	return err
}

func (r *Registry) markFailed(pending []string) {
	r.mu.Lock()
	r.state = stateFailed
	r.started = append([]string(nil), pending...)
	r.mu.Unlock()
}

func joinLifecycleErrors(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, cleanup)
}

func cleanupContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (r *Registry) stopModules(ctx context.Context, modules map[string]Module, ids []string) ([]string, error) {
	var stopErrs []error
	failed := make(map[string]struct{})
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if err := modules[id].Stop(ctx); err != nil {
			failed[id] = struct{}{}
			stopErrs = append(stopErrs, fmt.Errorf("stop module %q: %w", id, err))
		}
	}
	pending := make([]string, 0, len(failed))
	for _, id := range ids {
		if _, ok := failed[id]; ok {
			pending = append(pending, id)
		}
	}
	return pending, errors.Join(stopErrs...)
}

func (r *Registry) resolveOrderLocked() ([]string, error) {
	ids := make([]string, 0, len(r.modules))
	for id := range r.modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	const (
		unvisited = uint8(iota)
		visiting
		visited
	)
	colors := make(map[string]uint8, len(r.modules))
	order := make([]string, 0, len(r.modules))
	var visit func(string) error
	visit = func(id string) error {
		switch colors[id] {
		case visiting:
			return fmt.Errorf("module dependency cycle detected at %q", id)
		case visited:
			return nil
		}
		m, ok := r.modules[id]
		if !ok {
			return fmt.Errorf("module dependency %q is not registered", id)
		}
		colors[id] = visiting
		deps := append([]string(nil), m.Dependencies()...)
		sort.Strings(deps)
		for _, dep := range deps {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				return fmt.Errorf("module %q has an empty dependency", id)
			}
			if err := visit(dep); err != nil {
				return fmt.Errorf("resolve dependencies for module %q: %w", id, err)
			}
		}
		colors[id] = visited
		order = append(order, id)
		return nil
	}

	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (s registryState) String() string {
	switch s {
	case stateOpen:
		return "open"
	case stateStarting:
		return "starting"
	case stateRunning:
		return "running"
	case stateStopping:
		return "stopping"
	case stateStopped:
		return "stopped"
	case stateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
