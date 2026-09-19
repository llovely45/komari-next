// Package cache defines the storage-independent cache boundary used by
// modules. A cache miss is never a source-of-truth failure: callers must
// reload the value from PostgreSQL.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"
)

// ErrMiss is returned when a key is absent. It is the only valid cache miss
// signal and must remain distinguishable from backend failures.
var ErrMiss = errors.New("cache miss")

// ErrInvalidTTL indicates that a cache entry was given no usable lifetime.
var ErrInvalidTTL = errors.New("cache expiry must be positive")

var (
	errInvalidResult = errors.New("cache returned an invalid result")
	errNilLoader     = errors.New("cache loader is nil")
)

// Cache is the only cache API that modules should depend on.
type Cache interface {
	GetJSON(context.Context, string, any) (hit bool, err error)
	SetJSON(context.Context, string, any, time.Duration) error
	Delete(context.Context, ...string) error
	Close() error
}

// FailureOperation identifies the cache operation that failed before the
// source-of-truth loader was used.
type FailureOperation string

const (
	CacheReadFailure   FailureOperation = "read"
	CacheRefillFailure FailureOperation = "refill"
)

// ErrorReport is the observable form of a cache backend failure. Err is kept
// for metrics and injected reporters; the default logger deliberately emits
// only its type so credentials in backend errors cannot reach logs.
type ErrorReport struct {
	Operation FailureOperation
	Err       error
}

// ErrorReporter observes cache backend failures without coupling the cache
// port to a metrics implementation.
type ErrorReporter interface {
	ReportCacheError(context.Context, ErrorReport)
}

// ErrorReporterFunc adapts a function into an ErrorReporter.
type ErrorReporterFunc func(context.Context, ErrorReport)

func (f ErrorReporterFunc) ReportCacheError(ctx context.Context, report ErrorReport) {
	if f != nil {
		f(ctx, report)
	}
}

type loggingErrorReporter struct{}

func (loggingErrorReporter) ReportCacheError(ctx context.Context, report ErrorReport) {
	if report.Err == nil {
		return
	}
	logger.WarnContext(ctx, "cache", "cache backend failure; using source-of-truth data",
		"operation", string(report.Operation),
		"error_type", fmt.Sprintf("%T", report.Err),
	)
}

// Noop is the safe development and Redis-disabled implementation.
type Noop struct{}

func (Noop) GetJSON(context.Context, string, any) (bool, error) {
	return false, ErrMiss
}

func (Noop) SetJSON(_ context.Context, _ string, _ any, expiry time.Duration) error {
	if expiry <= 0 {
		return ErrInvalidTTL
	}
	return nil
}

func (Noop) Delete(context.Context, ...string) error {
	return nil
}

func (Noop) Close() error {
	return nil
}

// ReadThrough reads a JSON value from the cache and falls back to loader when
// the cache misses or reports a backend error. A successful source read is
// returned even when the best-effort cache fill fails.
func ReadThrough[T any](ctx context.Context, store Cache, key string, expiry time.Duration, loader func(context.Context) (T, error)) (T, error) {
	return ReadThroughWithReporter(ctx, store, key, expiry, loader, loggingErrorReporter{})
}

// ReadThroughWithReporter is ReadThrough with an injectable backend failure
// reporter. Cache misses are ordinary control flow and are never reported.
func ReadThroughWithReporter[T any](ctx context.Context, store Cache, key string, expiry time.Duration, loader func(context.Context) (T, error), reporter ErrorReporter) (T, error) {
	var zero T
	if store == nil {
		if loader == nil {
			return zero, errNilLoader
		}
		return loader(ctx)
	}

	var value T
	hit, err := store.GetJSON(ctx, key, &value)
	if hit && err == nil {
		return value, nil
	}
	if err == nil {
		return zero, errInvalidResult
	}
	if !errors.Is(err, ErrMiss) {
		if reporter == nil {
			reporter = loggingErrorReporter{}
		}
		reporter.ReportCacheError(ctx, ErrorReport{Operation: CacheReadFailure, Err: err})
	}
	if loader == nil {
		return zero, errNilLoader
	}

	loaded, err := loader(ctx)
	if err != nil {
		return zero, err
	}
	if expiry > 0 {
		if err := store.SetJSON(ctx, key, loaded, expiry); err != nil {
			if reporter == nil {
				reporter = loggingErrorReporter{}
			}
			reporter.ReportCacheError(ctx, ErrorReport{Operation: CacheRefillFailure, Err: err})
		}
	}
	return loaded, nil
}

// ReadThroughJSON is the descriptive alias for ReadThrough.
func ReadThroughJSON[T any](ctx context.Context, store Cache, key string, expiry time.Duration, loader func(context.Context) (T, error)) (T, error) {
	return ReadThrough(ctx, store, key, expiry, loader)
}
