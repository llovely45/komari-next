package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/cache"
)

func TestRetryMetricStoreConnectionStopsAfterRecovery(t *testing.T) {
	wantErr := errors.New("temporary connection failure")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		if attempts < 3 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry returned error after recovery: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestGuideNoRouteRejectsUnknownAPIAndRedirectsPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(noStoreAPIResponses())
	router.NoRoute(guideNoRoute("/install", "Not found in install mode", nil))

	apiResponse := httptest.NewRecorder()
	router.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
	if got := apiResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API cache control = %q, want no-store", got)
	}

	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/stale-guide", nil))
	if pageResponse.Code != http.StatusTemporaryRedirect {
		t.Fatalf("page status = %d, want %d", pageResponse.Code, http.StatusTemporaryRedirect)
	}
	if got := pageResponse.Header().Get("Location"); got != "/install" {
		t.Fatalf("redirect location = %q, want /install", got)
	}
}

func TestRetryMetricStoreConnectionReturnsLastError(t *testing.T) {
	wantErr := errors.New("connection unavailable")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestShutdownReturnsCleanupError(t *testing.T) {
	wantErr := errors.New("queued reports were not flushed")
	app := New(Options{})
	app.addCleanup("metric-report-batcher", func(_ context.Context) error {
		return wantErr
	})

	err := app.Shutdown()
	if !errors.Is(err, wantErr) {
		t.Fatalf("shutdown error = %v, want %v", err, wantErr)
	}
}

func TestInitCacheUsesBuiltinRedisWithoutExternalConfiguration(t *testing.T) {
	fake := &fakeBuiltinRedis{}
	previous := newBuiltinRedis
	newBuiltinRedis = func() (builtinRedis, error) { return fake, nil }
	t.Cleanup(func() { newBuiltinRedis = previous })

	app := New(Options{})
	if err := app.InitCache(); err != nil {
		t.Fatalf("InitCache: %v", err)
	}
	if app.Cache() != fake {
		t.Fatalf("cache = %T, want the built-in Redis instance", app.Cache())
	}
	if fake.pinged == 0 {
		t.Fatal("InitCache did not ping built-in Redis")
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !fake.closed {
		t.Fatal("shutdown did not close built-in Redis")
	}
}

func TestInitCacheFailsWhenBuiltinRedisIsUnavailable(t *testing.T) {
	previous := newBuiltinRedis
	newBuiltinRedis = func() (builtinRedis, error) {
		return &fakeBuiltinRedis{pingErr: errors.New("redis unavailable")}, nil
	}
	t.Cleanup(func() { newBuiltinRedis = previous })

	app := New(Options{})
	if err := app.InitCache(); err == nil {
		t.Fatal("InitCache succeeded without the built-in Redis service")
	}
	if _, ok := app.Cache().(cache.Noop); !ok {
		t.Fatalf("cache after failed initialization = %T, want cache.Noop", app.Cache())
	}
}

type fakeBuiltinRedis struct {
	cache.Noop
	pingErr error
	pinged  int
	closed  bool
}

func (f *fakeBuiltinRedis) Ping(context.Context) error {
	f.pinged++
	return f.pingErr
}

func (f *fakeBuiltinRedis) Close() error {
	f.closed = true
	return nil
}
