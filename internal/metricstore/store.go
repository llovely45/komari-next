package metricstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

var (
	store             *metric.Store
	storeMu           sync.RWMutex
	storeInitMu       sync.Mutex
	storeOperations   = newStoreOperationGate()
	compactOperations = newStoreOperationGate()
	sharedDBMu        sync.RWMutex
	sharedDB          *sql.DB
)

var ErrCompactInProgress = errors.New("metric store compact already in progress")

// SetSharedDatabase supplies the primary PostgreSQL pool to the metric store.
// Keeping this setter in the integration layer avoids an import cycle between
// dbcore's legacy migration package and the metric store package.
func SetSharedDatabase(db *sql.DB) {
	sharedDBMu.Lock()
	sharedDB = db
	sharedDBMu.Unlock()
}

func sharedDatabase() (*sql.DB, error) {
	sharedDBMu.RLock()
	db := sharedDB
	sharedDBMu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("shared PostgreSQL database is not initialized")
	}
	return db, nil
}

// ErrStructureUpgradeRequired reports that the configured store must be
// migrated by the restricted startup guide before it can be opened normally.
var ErrStructureUpgradeRequired = errors.New("metric store structure upgrade is required")

// openStore 按配置打开 metric store 并创建指标定义。
func openStore(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
	return openStoreWithDefaultRetention(ctx, cfg, defaultBuiltinMetricRetentionDays)
}

func openStoreWithDefaultRetention(ctx context.Context, cfg *MetricStoreConfig, defaultRetentionDays int) (*metric.Store, error) {
	metricCfg, err := buildMetricConfig(cfg, true)
	if err != nil {
		return nil, err
	}

	s, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open metric store: %w", err)
	}

	if err := createMetricDefinitionsWithDefaultRetention(ctx, s, defaultRetentionDays); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to create metric definitions: %w", err)
	}

	return s, nil
}

// OpenStoreForMigration opens a view over the shared target and uses the legacy data span
// as the initial retention for definitions that do not exist yet. Existing
// definitions keep their configured retention, including an explicit zero.
func OpenStoreForMigration(ctx context.Context, cfg *MetricStoreConfig, legacyRetentionDays int) (*metric.Store, error) {
	if legacyRetentionDays < defaultBuiltinMetricRetentionDays {
		legacyRetentionDays = defaultBuiltinMetricRetentionDays
	}
	return openStoreWithDefaultRetention(ctx, cfg, legacyRetentionDays)
}

// TestConnection 使用给定配置尝试连接 metrics 数据库（不影响当前运行的 store）。
// 仅打开连接并 Ping，不执行自动建表，连接成功后立即关闭。失败时返回可读错误。
func TestConnection(ctx context.Context, cfg *MetricStoreConfig) error {
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return err
	}

	s, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Ping(ctx)
}

// InitializeStore 初始化 metric store（启动时调用，可在失败后重试）。
func InitializeStore() error {
	storeInitMu.Lock()
	defer storeInitMu.Unlock()

	// A previous failed connection must remain retryable. The old sync.Once
	// implementation consumed the one-time call on failure and returned nil on
	// every later call while the store was still nil.
	storeMu.RLock()
	initialized := store != nil
	storeMu.RUnlock()
	if initialized {
		return nil
	}

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}

	// The metric store is always enabled and uses the shared PostgreSQL pool.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}

	storeMu.Lock()
	store = s
	storeMu.Unlock()

	logger.Infof("metricstore", "Metric store initialized successfully (driver=postgresql, table_prefix=%s)", cfg.TablePrefix)
	return nil
}

// Reload 根据最新配置热重载 metric store，无需重启进程。
// metric store 始终启用：用新配置打开并建表（内部已 Ping 校验连接），
// 成功后再替换运行中的 store，最后关闭旧实例。任何失败都会保留旧 store 不变。
//
// Reload only rebuilds the Store view over the same PostgreSQL pool; it never
// switches databases or copies metric history between backends.
func Reload(ctx context.Context) error {
	if err := storeOperations.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for metric store operations before reload: %w", err)
	}
	defer storeOperations.Release()
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}

	// Do not let the normal hot-reload path run AutoMigrate against an old
	// point-backed schema. That schema must go through the authenticated
	// structure-upgrade guide first; otherwise index creation can reference
	// columns such as resolution_id that do not exist yet.
	checkCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return err
	}
	checkStore, err := metric.Open(ctx, checkCfg)
	if err != nil {
		return err
	}
	needsRestructure, err := checkStore.NeedsRestructure(ctx)
	_ = checkStore.Close()
	if err != nil {
		return fmt.Errorf("inspect metric store structure: %w", err)
	}
	if needsRestructure {
		return fmt.Errorf("%w before hot reload", ErrStructureUpgradeRequired)
	}

	// 用新配置打开并建表（内部已 Ping 校验连接）。
	s, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}

	storeMu.Lock()
	old := store
	store = s
	storeMu.Unlock()
	if old != nil {
		if cerr := old.Close(); cerr != nil {
			logger.Errorf("metricstore", "Failed to close previous metric store on reload: %v", cerr)
		}
	}

	logger.Infof("metricstore", "Metric store reloaded successfully (driver=postgresql, table_prefix=%s)", cfg.TablePrefix)
	return nil
}

// GetStore 获取 metric store 实例。
func GetStore() *metric.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

// CloseStoreContext closes the current Store view without closing the shared
// PostgreSQL pool, which remains owned by dbcore.
func CloseStoreContext(ctx context.Context) error {
	if err := storeOperations.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for metric store operations before close: %w", err)
	}
	defer storeOperations.Release()

	storeMu.Lock()
	defer storeMu.Unlock()

	if store != nil {
		err := store.Close()
		store = nil
		return err
	}
	return nil
}
