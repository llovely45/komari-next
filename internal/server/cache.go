package server

import (
	"context"
	"fmt"
	"time"

	"github.com/komari-monitor/komari/internal/cache"
)

type builtinRedis interface {
	cache.Cache
	Ping(context.Context) error
}

var newBuiltinRedis = func() (builtinRedis, error) {
	return cache.NewRedis(cache.Options{URL: cache.BuiltinRedisURL})
}

// InitCache initializes the mandatory container-local Redis cache. Redis is a
// cache rather than the source of truth, but a missing built-in service is a
// deployment error and must not be silently hidden by a Noop implementation.
func (a *App) InitCache() error {
	if a.cacheStore != nil {
		return nil
	}

	redisCache, err := newBuiltinRedis()
	if err != nil {
		return fmt.Errorf("initialize built-in Redis: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = redisCache.Ping(pingCtx)
	cancel()
	if err != nil {
		_ = redisCache.Close()
		return fmt.Errorf("connect to built-in Redis: %w", err)
	}

	a.cacheStore = redisCache
	a.addCleanup("cache", func(context.Context) error { return redisCache.Close() })
	return nil
}
