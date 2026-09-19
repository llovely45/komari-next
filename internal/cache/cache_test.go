package cache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeBackend struct {
	values      map[string]string
	setKeys     []string
	setExpiries []time.Duration
	deletedKeys []string
	getErr      error
	setErr      error
	deleteErr   error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{values: make(map[string]string)}
}

func (f *fakeBackend) Get(_ context.Context, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	value, ok := f.values[key]
	if !ok {
		return "", ErrMiss
	}
	return value, nil
}

func (f *fakeBackend) Set(_ context.Context, key, value string, expiry time.Duration) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	f.setKeys = append(f.setKeys, key)
	f.setExpiries = append(f.setExpiries, expiry)
	return nil
}

func (f *fakeBackend) Delete(_ context.Context, keys ...string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedKeys = append(f.deletedKeys, keys...)
	for _, key := range keys {
		delete(f.values, key)
	}
	return nil
}

func TestRedisCacheJSONRoundTripUsesNamespacedKeyAndTTL(t *testing.T) {
	backend := newFakeBackend()
	cache := newRedisWithBackend(backend, "")
	ctx := context.Background()
	want := map[string]any{"name": "node-a", "online": true}

	if err := cache.SetJSON(ctx, "nodes/node-a", want, 2*time.Minute); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	if got := backend.setKeys[0]; got != "komari:v1:nodes/node-a" {
		t.Fatalf("stored key = %q, want namespaced key", got)
	}
	if got := backend.setExpiries[0]; got != 2*time.Minute {
		t.Fatalf("stored expiry = %s, want 2m", got)
	}

	var got map[string]any
	hit, err := cache.GetJSON(ctx, "nodes/node-a", &got)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !hit {
		t.Fatal("GetJSON reported a cache miss after SetJSON")
	}
	if got["name"] != want["name"] || got["online"] != want["online"] {
		t.Fatalf("decoded value = %#v, want %#v", got, want)
	}
}

func TestRedisCacheMissIsNotAnApplicationError(t *testing.T) {
	cache := newRedisWithBackend(newFakeBackend(), "komari:v1")
	var value map[string]string
	hit, err := cache.GetJSON(context.Background(), "missing", &value)
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("GetJSON miss error = %v, want errors.Is(err, ErrMiss)", err)
	}
	if hit {
		t.Fatal("GetJSON reported a hit for a missing key")
	}
}

func TestRedisCachePropagatesBackendErrorsAndDeletesNamespacedKeys(t *testing.T) {
	backend := newFakeBackend()
	wantErr := errors.New("redis unavailable")
	backend.getErr = wantErr
	cache := newRedisWithBackend(backend, "komari:v1")

	var value map[string]string
	hit, err := cache.GetJSON(context.Background(), "settings", &value)
	if hit {
		t.Fatal("GetJSON reported a hit for a backend error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetJSON error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrMiss) {
		t.Fatalf("backend error %v must remain distinct from ErrMiss", err)
	}

	backend.getErr = nil
	if err := cache.SetJSON(context.Background(), "settings", map[string]string{"mode": "public"}, time.Minute); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	if err := cache.Delete(context.Background(), "settings"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(backend.deletedKeys) != 1 || backend.deletedKeys[0] != "komari:v1:settings" {
		t.Fatalf("deleted keys = %#v, want namespaced settings key", backend.deletedKeys)
	}

	deleteErr := errors.New("redis delete unavailable")
	backend.deleteErr = deleteErr
	if err := cache.Delete(context.Background(), "settings"); !errors.Is(err, deleteErr) {
		t.Fatalf("Delete error = %v, want %v", err, deleteErr)
	}
}

func TestRedisCacheRejectsNonPositiveTTL(t *testing.T) {
	backend := newFakeBackend()
	cache := newRedisWithBackend(backend, "komari:v1")

	for _, expiry := range []time.Duration{0, -time.Second} {
		if err := cache.SetJSON(context.Background(), "settings", map[string]string{"mode": "public"}, expiry); err == nil {
			t.Fatalf("SetJSON expiry %s returned nil", expiry)
		}
	}
	if len(backend.setKeys) != 0 {
		t.Fatalf("backend received Set for invalid TTL: %#v", backend.setKeys)
	}
}

func TestNoopCacheAlwaysMisses(t *testing.T) {
	var value map[string]string
	cache := Noop{}
	hit, err := cache.GetJSON(context.Background(), "settings", &value)
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("Noop GetJSON error = %v, want errors.Is(err, ErrMiss)", err)
	}
	if hit {
		t.Fatal("Noop cache reported a hit")
	}
	if err := cache.SetJSON(context.Background(), "settings", value, time.Minute); err != nil {
		t.Fatalf("Noop SetJSON: %v", err)
	}
	if err := cache.Delete(context.Background(), "settings"); err != nil {
		t.Fatalf("Noop Delete: %v", err)
	}
	if err := cache.SetJSON(context.Background(), "settings", value, 0); err == nil {
		t.Fatal("Noop SetJSON accepted a non-positive TTL")
	}
}

func TestNewRedisUsesURLWithoutDialingAndBoundsTimeouts(t *testing.T) {
	cache, err := NewRedis(Options{
		URL:          "redis://127.0.0.1:1/0",
		DialTimeout:  time.Hour,
		ReadTimeout:  -time.Second,
		WriteTimeout: 0,
	})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	backend, ok := cache.backend.(*goRedisBackend)
	if !ok {
		t.Fatalf("backend type = %T, want private go-redis adapter", cache.backend)
	}
	client, ok := backend.client.(*redis.Client)
	if !ok {
		t.Fatalf("client type = %T, want *redis.Client", backend.client)
	}
	options := client.Options()
	for name, timeout := range map[string]time.Duration{
		"dial":  options.DialTimeout,
		"read":  options.ReadTimeout,
		"write": options.WriteTimeout,
	} {
		if timeout <= 0 || timeout > maxRedisTimeout {
			t.Fatalf("%s timeout = %s, want 0 < timeout <= %s", name, timeout, maxRedisTimeout)
		}
	}
	if cache.prefix != defaultPrefix {
		t.Fatalf("prefix = %q, want default %q", cache.prefix, defaultPrefix)
	}
}

func TestReadThroughLoadsOnMissAndFillsAfterSourceSuccess(t *testing.T) {
	backend := newFakeBackend()
	cache := newRedisWithBackend(backend, "komari:v1")
	loads := 0

	got, err := ReadThrough(context.Background(), cache, "nodes/node-a", time.Minute, func(context.Context) (map[string]string, error) {
		loads++
		return map[string]string{"name": "node-a"}, nil
	})
	if err != nil {
		t.Fatalf("ReadThrough: %v", err)
	}
	if loads != 1 || got["name"] != "node-a" {
		t.Fatalf("loads=%d got=%#v, want one source load and node-a", loads, got)
	}
	if _, ok := backend.values["komari:v1:nodes/node-a"]; !ok {
		t.Fatalf("source result was not best-effort filled into the namespaced cache")
	}
}

func TestReadThroughLoadsOnBackendErrorAndReturnsSourceResultWhenSetFails(t *testing.T) {
	backend := newFakeBackend()
	getErr := errors.New("redis read unavailable")
	backend.getErr = getErr
	setErr := errors.New("redis write unavailable")
	backend.setErr = setErr
	cache := newRedisWithBackend(backend, "komari:v1")

	got, err := ReadThrough(context.Background(), cache, "settings", time.Minute, func(context.Context) (map[string]string, error) {
		return map[string]string{"mode": "public"}, nil
	})
	if err != nil {
		t.Fatalf("ReadThrough returned source error after cache failures: %v", err)
	}
	if got["mode"] != "public" {
		t.Fatalf("ReadThrough result = %#v, want source result", got)
	}
	if len(backend.setKeys) != 0 {
		t.Fatalf("failed Set should not be recorded as successful: %#v", backend.setKeys)
	}
}

func TestReadThroughReportsBackendReadAndRefillFailures(t *testing.T) {
	backend := newFakeBackend()
	readErr := errors.New("redis read unavailable")
	writeErr := errors.New("redis write unavailable")
	backend.getErr = readErr
	backend.setErr = writeErr
	cache := newRedisWithBackend(backend, "komari:v1")
	var reports []ErrorReport
	reporter := ErrorReporterFunc(func(_ context.Context, report ErrorReport) {
		reports = append(reports, report)
	})

	got, err := ReadThroughWithReporter(context.Background(), cache, "settings", time.Minute, func(context.Context) (map[string]string, error) {
		return map[string]string{"mode": "public"}, nil
	}, reporter)
	if err != nil {
		t.Fatalf("ReadThroughWithReporter: %v", err)
	}
	if got["mode"] != "public" {
		t.Fatalf("ReadThroughWithReporter result = %#v, want source result", got)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %#v, want read and refill reports", reports)
	}
	if reports[0].Operation != CacheReadFailure || !errors.Is(reports[0].Err, readErr) {
		t.Fatalf("read report = %#v, want read operation and original error", reports[0])
	}
	if reports[1].Operation != CacheRefillFailure || !errors.Is(reports[1].Err, writeErr) {
		t.Fatalf("refill report = %#v, want refill operation and original error", reports[1])
	}
}

func TestReadThroughDoesNotReportOrdinaryCacheMiss(t *testing.T) {
	cache := newRedisWithBackend(newFakeBackend(), "komari:v1")
	var reports []ErrorReport
	reporter := ErrorReporterFunc(func(_ context.Context, report ErrorReport) {
		reports = append(reports, report)
	})

	if _, err := ReadThroughWithReporter(context.Background(), cache, "settings", time.Minute, func(context.Context) (map[string]string, error) {
		return map[string]string{"mode": "public"}, nil
	}, reporter); err != nil {
		t.Fatalf("ReadThroughWithReporter: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports = %#v, want no report for a cache miss", reports)
	}
}

func TestReadThroughDefaultReporterLogsStructuredSafeWarning(t *testing.T) {
	const secret = "redis-fix-round-secret"
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	backend := newFakeBackend()
	backend.getErr = errors.New("redis auth failed for redis://:" + secret + "@cache.example/0")
	cache := newRedisWithBackend(backend, "komari:v1")
	if _, err := ReadThrough(context.Background(), cache, "settings", time.Minute, func(context.Context) (map[string]string, error) {
		return map[string]string{"mode": "public"}, nil
	}); err != nil {
		t.Fatalf("ReadThrough: %v", err)
	}

	logOutput := output.String()
	if !strings.Contains(logOutput, "cache backend failure") || !strings.Contains(logOutput, "operation=read") {
		t.Fatalf("log output = %q, want structured cache read warning", logOutput)
	}
	if strings.Contains(logOutput, secret) {
		t.Fatalf("log output contains a Redis credential: %q", logOutput)
	}
}

func TestReadThroughReturnsLoaderErrorAndDoesNotFill(t *testing.T) {
	backend := newFakeBackend()
	cache := newRedisWithBackend(backend, "komari:v1")
	wantErr := errors.New("database unavailable")

	got, err := ReadThrough(context.Background(), cache, "settings", time.Minute, func(context.Context) (map[string]string, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadThrough error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("ReadThrough result = %#v, want nil on loader error", got)
	}
	if len(backend.setKeys) != 0 {
		t.Fatalf("loader error must not fill cache: %#v", backend.setKeys)
	}
}
