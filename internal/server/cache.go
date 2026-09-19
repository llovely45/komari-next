package server

import (
	"context"
	"strings"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/cache"
	logger "github.com/komari-monitor/komari/utils/log"
)

// InitCache initializes the optional Redis cache. Redis is deliberately
// best-effort: an invalid URL or an unavailable cache configuration must not
// prevent the PostgreSQL/SQLite source of truth from starting.
func (a *App) InitCache() error {
	if a.cacheStore != nil {
		return nil
	}

	redisURL := strings.TrimSpace(flags.RedisURL)
	if redisURL == "" {
		a.cacheStore = cache.Noop{}
		return nil
	}

	redisCache, err := cache.NewRedis(cache.Options{URL: redisURL})
	if err != nil {
		// Do not log the URL or the parser error verbatim: Redis URLs may carry
		// credentials, and configuration errors must not turn them into logs.
		logger.Warn("server", "Redis cache disabled; falling back to source-of-truth reads", "reason", "invalid Redis URL")
		a.cacheStore = cache.Noop{}
		return nil
	}

	a.cacheStore = redisCache
	a.addCleanup("cache", func(context.Context) error { return redisCache.Close() })
	return nil
}
