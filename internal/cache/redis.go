package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// BuiltinRedisURL is the only Redis endpoint used by the application. The
	// Docker image starts Redis on this container-local address; it is never
	// read from user configuration.
	BuiltinRedisURL     = "redis://127.0.0.1:6379/0"
	defaultPrefix       = "komari:v1"
	defaultDialTimeout  = 5 * time.Second
	defaultReadTimeout  = 5 * time.Second
	defaultWriteTimeout = 5 * time.Second
	maxRedisTimeout     = 10 * time.Second
)

type backend interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string, time.Duration) error
	Delete(context.Context, ...string) error
}

type pingBackend interface {
	Ping(context.Context) error
}

type prefixBackend interface {
	DeletePrefix(context.Context, string) error
}

// Redis is a Cache backed by a Redis client. The raw go-redis client is kept
// inside this adapter and is never exposed to modules.
type Redis struct {
	backend   backend
	prefix    string
	close     func() error
	closeOnce sync.Once
	closeErr  error
}

// Options controls construction of a Redis cache from a URL.
type Options struct {
	URL          string
	Prefix       string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewRedis creates a Redis cache from a redis://, rediss://, or unix:// URL.
// Creating the adapter is lazy: it does not Ping or otherwise contact Redis.
func NewRedis(options Options) (*Redis, error) {
	if strings.TrimSpace(options.URL) == "" {
		return nil, errors.New("redis URL is empty")
	}
	redisOptions, err := redis.ParseURL(strings.TrimSpace(options.URL))
	if err != nil {
		return nil, errors.New("invalid Redis URL")
	}
	if options.DB != 0 {
		redisOptions.DB = options.DB
	}
	if options.DialTimeout != 0 {
		redisOptions.DialTimeout = options.DialTimeout
	}
	if options.ReadTimeout != 0 {
		redisOptions.ReadTimeout = options.ReadTimeout
	}
	if options.WriteTimeout != 0 {
		redisOptions.WriteTimeout = options.WriteTimeout
	}
	redisOptions.DialTimeout = boundedTimeout(redisOptions.DialTimeout, defaultDialTimeout)
	redisOptions.ReadTimeout = boundedTimeout(redisOptions.ReadTimeout, defaultReadTimeout)
	redisOptions.WriteTimeout = boundedTimeout(redisOptions.WriteTimeout, defaultWriteTimeout)

	client := redis.NewClient(redisOptions)
	cache := newRedisWithBackend(&goRedisBackend{client: client}, options.Prefix)
	cache.close = client.Close
	return cache, nil
}

// Open is retained as a descriptive alias for NewRedis.
func Open(options Options) (*Redis, error) {
	return NewRedis(options)
}

func boundedTimeout(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value > maxRedisTimeout {
		return maxRedisTimeout
	}
	return value
}

func newRedisWithBackend(store backend, prefix string) *Redis {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), ":")
	if prefix == "" {
		prefix = defaultPrefix
	}
	return &Redis{backend: store, prefix: prefix}
}

func (c *Redis) key(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("cache key is empty")
	}
	return c.prefix + ":" + raw, nil
}

func (c *Redis) GetJSON(ctx context.Context, rawKey string, dst any) (bool, error) {
	if dst == nil {
		return false, errors.New("cache destination is nil")
	}
	key, err := c.key(rawKey)
	if err != nil {
		return false, err
	}
	value, err := c.backend.Get(ctx, key)
	if errors.Is(err, ErrMiss) {
		return false, err
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(value), dst); err != nil {
		return false, fmt.Errorf("decode cache key %q: %w", rawKey, err)
	}
	return true, nil
}

func (c *Redis) SetJSON(ctx context.Context, rawKey string, value any, expiry time.Duration) error {
	if expiry <= 0 {
		return ErrInvalidTTL
	}
	key, err := c.key(rawKey)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode cache key %q: %w", rawKey, err)
	}
	return c.backend.Set(ctx, key, string(encoded), expiry)
}

func (c *Redis) Delete(ctx context.Context, rawKeys ...string) error {
	if len(rawKeys) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		key, err := c.key(rawKey)
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}
	return c.backend.Delete(ctx, keys...)
}

// DeletePrefix removes all keys below a namespaced raw-key prefix using Redis
// SCAN, avoiding a blocking KEYS command.
func (c *Redis) DeletePrefix(ctx context.Context, rawPrefix string) error {
	rawPrefix = strings.TrimSpace(rawPrefix)
	if rawPrefix == "" {
		return errors.New("cache prefix is empty")
	}
	store, ok := c.backend.(prefixBackend)
	if !ok {
		return errors.New("cache backend does not support prefix deletion")
	}
	return store.DeletePrefix(ctx, c.prefix+":"+rawPrefix)
}

func (c *Redis) Close() error {
	c.closeOnce.Do(func() {
		if c.close != nil {
			c.closeErr = c.close()
		}
	})
	return c.closeErr
}

// Ping verifies that the built-in Redis service is reachable. It is kept
// separate from NewRedis so cache unit tests can construct an adapter without
// a live Redis daemon, while application startup can make Redis mandatory.
func (c *Redis) Ping(ctx context.Context) error {
	if c == nil || c.backend == nil {
		return errors.New("redis backend is nil")
	}
	backend, ok := c.backend.(pingBackend)
	if !ok {
		return nil
	}
	return backend.Ping(ctx)
}

type goRedisBackend struct {
	client redis.UniversalClient
}

func (b *goRedisBackend) Get(ctx context.Context, key string) (string, error) {
	value, err := b.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrMiss
	}
	return value, err
}

func (b *goRedisBackend) Set(ctx context.Context, key, value string, expiry time.Duration) error {
	return b.client.Set(ctx, key, value, expiry).Err()
}

func (b *goRedisBackend) Delete(ctx context.Context, keys ...string) error {
	return b.client.Del(ctx, keys...).Err()
}

func (b *goRedisBackend) DeletePrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		keys, next, err := b.client.Scan(ctx, cursor, prefix+"*", 256).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := b.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func (b *goRedisBackend) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}
