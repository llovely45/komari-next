package metricstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/komari-monitor/komari/internal/cache"
	"github.com/komari-monitor/komari/pkg/metric"
)

const (
	metricCachePrefix        = "metricstore:"
	metricDefinitionsKey     = metricCachePrefix + "definitions"
	recentMetricCacheWindow  = 10 * time.Minute
	recentMetricCacheTTL     = 5 * time.Second
	historicalMetricCacheTTL = 10 * time.Minute
	metricCacheTimeBucket    = 5 * time.Second
	metricDefinitionsTTL     = 15 * time.Minute
	maxMetricQueryCacheBytes = 1 << 20
)

var metricCache = struct {
	sync.RWMutex
	store cache.Cache
}{store: cache.Noop{}}

// SetCache installs the process-wide cache used by metric query helpers.
// The server sets this only after its mandatory Redis connection succeeds.
func SetCache(store cache.Cache) {
	if store == nil {
		store = cache.Noop{}
	}
	metricCache.Lock()
	metricCache.store = store
	metricCache.Unlock()
}

func currentMetricCache() cache.Cache {
	metricCache.RLock()
	store := metricCache.store
	metricCache.RUnlock()
	return store
}

// ReadCached uses Redis as a best-effort read-through cache. Source-of-truth
// errors are returned; Redis errors fall back to the loader.
func ReadCached[T any](ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, error)) (T, error) {
	return cache.ReadThrough(ctx, currentMetricCache(), key, ttl, loader)
}

func readMetricQueryCached[T any](ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, error)) (T, error) {
	store := currentMetricCache()
	return cache.ReadThrough(ctx, sizeLimitedCache{Cache: store, maxBytes: maxMetricQueryCacheBytes}, key, ttl, loader)
}

type sizeLimitedCache struct {
	cache.Cache
	maxBytes int
}

func (c sizeLimitedCache) SetJSON(ctx context.Context, key string, value any, expiry time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > c.maxBytes {
		return nil
	}
	return c.Cache.SetJSON(ctx, key, value, expiry)
}

// CacheKey builds a compact deterministic key for the supplied query identity.
func CacheKey(namespace string, identity any) string {
	encoded, err := json.Marshal(identity)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%T:%v", identity, identity))
	}
	digest := sha256.Sum256(encoded)
	return metricCachePrefix + namespace + ":" + hex.EncodeToString(digest[:])
}

// QueryCacheKey normalizes moving query windows into five-second slices while
// leaving older, stable ranges exact so distinct historical requests do not
// collide.
func QueryCacheKey(namespace string, identity any, start, end, now time.Time) string {
	start = start.UTC()
	end = end.UTC()
	if end.After(now.UTC().Add(-recentMetricCacheWindow)) {
		start = start.Truncate(metricCacheTimeBucket)
		end = end.Truncate(metricCacheTimeBucket)
	}
	return CacheKey("query:"+namespace, struct {
		Identity any       `json:"identity"`
		Start    time.Time `json:"start"`
		End      time.Time `json:"end"`
	}{Identity: identity, Start: start, End: end})
}

// QueryCacheTTL gives live metric windows a short freshness bound and keeps
// completed historical ranges hot longer.
func QueryCacheTTL(end, now time.Time) time.Duration {
	if end.After(now.UTC().Add(-recentMetricCacheWindow)) {
		return recentMetricCacheTTL
	}
	return historicalMetricCacheTTL
}

// GetMetricDefinitions returns cached definitions, including their metadata
// and retention settings.
func GetMetricDefinitions(ctx context.Context) ([]metric.Definition, error) {
	store := GetStore()
	if store == nil {
		return nil, fmt.Errorf("metric store not initialized")
	}
	return ReadCached(ctx, metricDefinitionsKey, metricDefinitionsTTL, store.ListMetrics)
}

// GetMetricDefinitionsByName returns the requested subset of cached metric
// definitions. Missing names are omitted so callers can preserve their own
// validation behavior.
func GetMetricDefinitionsByName(ctx context.Context, names []string) (map[string]metric.Definition, error) {
	all, err := GetMetricDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	out := make(map[string]metric.Definition, len(wanted))
	for _, definition := range all {
		if _, ok := wanted[definition.Name]; ok {
			out[definition.Name] = definition
		}
	}
	return out, nil
}

// QueryBatchCached caches exact raw samples for repeated, equivalent queries.
func QueryBatchCached(ctx context.Context, store *metric.Store, query metric.BatchQuery) (map[string][]metric.Point, error) {
	now := time.Now().UTC()
	key := QueryCacheKey(metricQueryNamespace("raw", query.MetricNames), struct {
		MetricNames []string          `json:"metric_names"`
		EntityIDs   []string          `json:"entity_ids"`
		Tags        map[string]string `json:"tags"`
		Order       metric.Order      `json:"order"`
	}{query.MetricNames, query.EntityIDs, query.Tags, query.Order}, query.Start, query.End, now)
	return readMetricQueryCached(ctx, key, QueryCacheTTL(query.End, now), func(ctx context.Context) (map[string][]metric.Point, error) {
		return store.QueryBatch(ctx, query)
	})
}

// SeriesBatchCached caches downsampled metric series for repeated queries.
func SeriesBatchCached(ctx context.Context, store *metric.Store, query metric.BatchSeriesQuery, now time.Time) (metric.BatchSeriesResult, error) {
	metricNames := make([]string, 0, len(query.Specs))
	for _, spec := range query.Specs {
		metricNames = append(metricNames, spec.MetricName)
	}
	key := QueryCacheKey(metricQueryNamespace("series", metricNames), struct {
		Specs     []metric.BatchSeriesSpec `json:"specs"`
		EntityIDs []string                 `json:"entity_ids"`
		Tags      map[string]string        `json:"tags"`
		Order     metric.Order             `json:"order"`
	}{query.Specs, query.EntityIDs, query.Tags, query.Order}, query.Start, query.End, now)
	return readMetricQueryCached(ctx, key, QueryCacheTTL(query.End, now), func(ctx context.Context) (metric.BatchSeriesResult, error) {
		return store.SeriesBatch(ctx, query, now)
	})
}

func metricQueryNamespace(base string, metricNames []string) string {
	for _, name := range metricNames {
		if name == MetricPingLatency || name == MetricPingLoss {
			return base + ":ping"
		}
	}
	return base + ":system"
}

// DeleteCacheKey removes one metric cache entry. It is best-effort because
// Redis is never the source of truth.
func DeleteCacheKey(ctx context.Context, key string) {
	if err := currentMetricCache().Delete(ctx, key); err != nil {
		cache.ReportCacheFailure(ctx, cache.CacheInvalidationFailure, err)
	}
}

// InvalidateMetricQueryCache clears derived metric query results after an
// administrative delete or retention change.
func InvalidateMetricQueryCache(ctx context.Context) {
	deleteMetricCachePrefix(ctx, metricCachePrefix+"query:")
}

// InvalidatePingStatsCache clears the short-lived per-node ping summary cache
// after ping task configuration changes.
func InvalidatePingStatsCache(ctx context.Context) {
	deleteMetricCachePrefix(ctx, metricCachePrefix+"pingstats:")
}

// InvalidatePingQueryCache clears cached ping histories and aggregate series
// after ping data is deleted.
func InvalidatePingQueryCache(ctx context.Context) {
	deleteMetricCachePrefix(ctx, metricCachePrefix+"query:ping:records:")
	deleteMetricCachePrefix(ctx, metricCachePrefix+"query:raw:ping:")
	deleteMetricCachePrefix(ctx, metricCachePrefix+"query:series:ping:")
}

// InvalidateMetricDefinitions clears cached definitions and query results that
// embed their metadata.
func InvalidateMetricDefinitions(ctx context.Context) {
	deleteMetricCachePrefix(ctx, metricCachePrefix+"definitions")
	InvalidateMetricQueryCache(ctx)
}

func deleteMetricCachePrefix(ctx context.Context, prefix string) {
	deleter, ok := currentMetricCache().(interface {
		DeletePrefix(context.Context, string) error
	})
	if !ok {
		return
	}
	if err := deleter.DeletePrefix(ctx, prefix); err != nil {
		cache.ReportCacheFailure(ctx, cache.CacheInvalidationFailure, err)
	}
}

// MetricPingStatsCacheKey is shared by the RPC layer and ping ingestion so a
// newly stored ping result can invalidate the matching summary.
func MetricPingStatsCacheKey(entityID string) string {
	return CacheKey("pingstats", entityID)
}
