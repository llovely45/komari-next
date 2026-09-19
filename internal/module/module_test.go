package module

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testModule struct {
	id       string
	deps     []string
	events   *[]string
	initErr  error
	startErr error
	stopErr  error
	startFn  func(context.Context) error
	stopFn   func(context.Context) error
	initHost *Host
}

func (m *testModule) ID() string { return m.id }

func (m *testModule) Dependencies() []string { return append([]string(nil), m.deps...) }

func (m *testModule) Init(_ context.Context, host Host) error {
	if m.initHost != nil {
		*m.initHost = host
	}
	*m.events = append(*m.events, "init:"+m.id)
	return m.initErr
}

func (m *testModule) Start(ctx context.Context) error {
	*m.events = append(*m.events, "start:"+m.id)
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return m.startErr
}

func (m *testModule) Stop(ctx context.Context) error {
	*m.events = append(*m.events, "stop:"+m.id)
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return m.stopErr
}

type blockingModule struct {
	started     chan<- struct{}
	release     <-chan struct{}
	stopStarted chan<- struct{}
	stopRelease <-chan struct{}
	starts      atomic.Int32
	stops       atomic.Int32
	stopErr     error
}

type observedDoneContext struct {
	context.Context
	observed chan<- struct{}
	once     sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		c.observed <- struct{}{}
	})
	return c.Context.Done()
}

func (m *blockingModule) ID() string { return "blocking" }

func (m *blockingModule) Dependencies() []string { return nil }

func (m *blockingModule) Init(context.Context, Host) error { return nil }

func (m *blockingModule) Start(ctx context.Context) error {
	m.starts.Add(1)
	if m.started != nil {
		m.started <- struct{}{}
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *blockingModule) Stop(ctx context.Context) error {
	m.stops.Add(1)
	if m.stopStarted != nil {
		m.stopStarted <- struct{}{}
	}
	if m.stopRelease != nil {
		select {
		case <-m.stopRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.stopErr
}

func TestRegistryPassesLoggerAndExtensionPointToModules(t *testing.T) {
	logger := slog.Default()
	host := Host{
		Logger: logger,
		Extensions: ExtensionPoint{
			Version: ExtensionPointVersion,
		},
	}
	var gotHost Host
	var events []string
	registry := NewRegistry(host)
	if err := registry.Register(&testModule{id: "host-check", events: &events, initHost: &gotHost}); err != nil {
		t.Fatal(err)
	}

	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if gotHost.Logger != logger {
		t.Fatalf("logger = %p, want %p", gotHost.Logger, logger)
	}
	if gotHost.Extensions.Version != ExtensionPointVersion {
		t.Fatalf("extension point version = %d, want %d", gotHost.Extensions.Version, ExtensionPointVersion)
	}
}

func TestRegistryStartsDependenciesFirstAndStopsInReverse(t *testing.T) {
	var events []string
	registry := NewRegistry(Host{})

	if err := registry.Register(&testModule{id: "metrics", deps: []string{"database"}, events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "database", events: &events}); err != nil {
		t.Fatal(err)
	}

	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if want := []string{"init:database", "init:metrics", "start:database", "start:metrics"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("start events = %#v, want %#v", events, want)
	}

	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if want := []string{"init:database", "init:metrics", "start:database", "start:metrics", "stop:metrics", "stop:database"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("all events = %#v, want %#v", events, want)
	}
}

func TestRegistryOrdersIndependentModulesDeterministically(t *testing.T) {
	var events []string
	registry := NewRegistry(Host{})
	for _, id := range []string{"zeta", "alpha", "middle"} {
		if err := registry.Register(&testModule{id: id, events: &events}); err != nil {
			t.Fatal(err)
		}
	}

	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	want := []string{
		"init:alpha", "init:middle", "init:zeta",
		"start:alpha", "start:middle", "start:zeta",
		"stop:zeta", "stop:middle", "stop:alpha",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRegistryRejectsDuplicateID(t *testing.T) {
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{id: "database"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "database"}); err == nil {
		t.Fatal("duplicate registration unexpectedly succeeded")
	}
}

func TestRegistryRejectsMissingDependencyAndCycle(t *testing.T) {
	t.Run("missing dependency", func(t *testing.T) {
		registry := NewRegistry(Host{})
		if err := registry.Register(&testModule{id: "metrics", deps: []string{"database"}}); err != nil {
			t.Fatal(err)
		}
		if err := registry.Start(context.Background()); err == nil {
			t.Fatal("start unexpectedly succeeded with missing dependency")
		}
	})

	t.Run("cycle", func(t *testing.T) {
		registry := NewRegistry(Host{})
		if err := registry.Register(&testModule{id: "a", deps: []string{"b"}}); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(&testModule{id: "b", deps: []string{"a"}}); err != nil {
			t.Fatal(err)
		}
		if err := registry.Start(context.Background()); err == nil {
			t.Fatal("start unexpectedly succeeded with cyclic dependencies")
		}
	})
}

func TestRegistryRollsBackAllInitializedModulesWhenStartFails(t *testing.T) {
	var events []string
	startErr := errors.New("metrics unavailable")
	cleanupErr := errors.New("metrics cleanup failed")
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{id: "database", events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "metrics", deps: []string{"database"}, events: &events, startErr: startErr, stopErr: cleanupErr}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "cache", deps: []string{"metrics"}, events: &events}); err != nil {
		t.Fatal(err)
	}

	err := registry.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("start error = %v, want %v", err, startErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("start error = %v, want cleanup error %v", err, cleanupErr)
	}
	if want := []string{
		"init:database", "init:metrics", "init:cache",
		"start:database", "start:metrics",
		"stop:cache", "stop:metrics", "stop:database",
	}; !reflect.DeepEqual(events, want) {
		t.Fatalf("rollback events = %#v, want %#v", events, want)
	}
}

func TestRegistryContinuesStoppingAfterError(t *testing.T) {
	var events []string
	alphaErr := errors.New("alpha cleanup failed")
	betaErr := errors.New("beta cleanup failed")
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{id: "alpha", events: &events, stopErr: alphaErr}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "beta", deps: []string{"alpha"}, events: &events, stopErr: betaErr}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "gamma", deps: []string{"beta"}, events: &events}); err != nil {
		t.Fatal(err)
	}

	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := registry.Stop(context.Background())
	if !errors.Is(err, alphaErr) {
		t.Fatalf("stop error = %v, want alpha error %v", err, alphaErr)
	}
	if !errors.Is(err, betaErr) {
		t.Fatalf("stop error = %v, want beta error %v", err, betaErr)
	}
	if want := []string{
		"init:alpha", "init:beta", "init:gamma",
		"start:alpha", "start:beta", "start:gamma",
		"stop:gamma", "stop:beta", "stop:alpha",
	}; !reflect.DeepEqual(events, want) {
		t.Fatalf("stop events = %#v, want %#v", events, want)
	}
}

func TestRegistryRollsBackInitializedModulesWhenInitFails(t *testing.T) {
	var events []string
	wantErr := errors.New("metrics configuration invalid")
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{id: "database", events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testModule{id: "metrics", deps: []string{"database"}, events: &events, initErr: wantErr}); err != nil {
		t.Fatal(err)
	}

	err := registry.Start(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("start error = %v, want %v", err, wantErr)
	}
	if want := []string{"init:database", "init:metrics", "stop:database"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("init rollback events = %#v, want %#v", events, want)
	}
}

func TestRegistryRollbackUsesUsableCleanupContextAfterStartCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var events []string
	var stopContext context.Context
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{
		id:     "database",
		events: &events,
		startFn: func(ctx context.Context) error {
			return ctx.Err()
		},
		stopFn: func(ctx context.Context) error {
			stopContext = ctx
			return ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := registry.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("start error = %v, want context.Canceled", err)
	}
	if stopContext == nil {
		t.Fatal("rollback did not call Stop")
	}
	if err := stopContext.Err(); err != nil {
		t.Fatalf("rollback cleanup context error = %v, want usable context", err)
	}
}

func TestRegistryRollbackRetainsFailedCleanupForRetry(t *testing.T) {
	startErr := errors.New("module start failed")
	cleanupErr := errors.New("module cleanup failed")
	var events []string
	stopCalls := 0
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{
		id:       "database",
		events:   &events,
		startErr: startErr,
		stopFn: func(context.Context) error {
			stopCalls++
			if stopCalls == 1 {
				return cleanupErr
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := registry.Start(context.Background())
	if !errors.Is(err, startErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("start error = %v, want start and cleanup errors", err)
	}
	if stopCalls != 1 {
		t.Fatalf("rollback stop calls = %d, want 1", stopCalls)
	}

	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if stopCalls != 2 {
		t.Fatalf("cleanup stop calls after retry = %d, want 2", stopCalls)
	}
}

func TestRegistryKeepsFailedStopRetryable(t *testing.T) {
	cleanupErr := errors.New("module cleanup failed")
	var events []string
	stopCalls := 0
	registry := NewRegistry(Host{})
	if err := registry.Register(&testModule{
		id:     "database",
		events: &events,
		stopFn: func(context.Context) error {
			stopCalls++
			if stopCalls == 1 {
				return cleanupErr
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := registry.Stop(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("first stop error = %v, want %v", err, cleanupErr)
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("stop retry: %v", err)
	}
	if stopCalls != 2 {
		t.Fatalf("stop calls after retry = %d, want 2", stopCalls)
	}
}

func TestRegistryRejectsConcurrentStartAndStop(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mod := &blockingModule{started: started, release: release}
	registry := NewRegistry(Host{})
	if err := registry.Register(mod); err != nil {
		t.Fatal(err)
	}

	firstStart := make(chan error, 1)
	go func() {
		firstStart <- registry.Start(context.Background())
	}()
	<-started

	if err := registry.Start(context.Background()); err == nil {
		t.Fatal("concurrent start unexpectedly succeeded")
	}
	if err := registry.Stop(context.Background()); err == nil {
		t.Fatal("stop during start unexpectedly succeeded")
	}

	close(release)
	if err := <-firstStart; err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := mod.starts.Load(); got != 1 {
		t.Fatalf("start calls = %d, want 1", got)
	}
	if got := mod.stops.Load(); got != 1 {
		t.Fatalf("stop calls = %d, want 1", got)
	}
}

func TestRegistryWaitsForConcurrentStopAndReturnsFinalError(t *testing.T) {
	stopStarted := make(chan struct{}, 1)
	stopRelease := make(chan struct{})
	wantErr := errors.New("stop failed")
	mod := &blockingModule{
		stopStarted: stopStarted,
		stopRelease: stopRelease,
		stopErr:     wantErr,
	}
	registry := NewRegistry(Host{})
	if err := registry.Register(mod); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	firstStop := make(chan error, 1)
	go func() {
		firstStop <- registry.Stop(context.Background())
	}()
	<-stopStarted

	secondStop := make(chan error, 1)
	waitEntered := make(chan struct{}, 1)
	secondContext := &observedDoneContext{Context: context.Background(), observed: waitEntered}
	go func() {
		secondStop <- registry.Stop(secondContext)
	}()
	select {
	case <-waitEntered:
	case <-time.After(time.Second):
		t.Fatal("concurrent stop did not enter the wait path")
	}

	close(stopRelease)
	if err := <-firstStop; !errors.Is(err, wantErr) {
		t.Fatalf("first stop error = %v, want %v", err, wantErr)
	}
	if err := <-secondStop; !errors.Is(err, wantErr) {
		t.Fatalf("second stop error = %v, want %v", err, wantErr)
	}
	if got := mod.stops.Load(); got != 1 {
		t.Fatalf("stop calls = %d, want 1", got)
	}
}
