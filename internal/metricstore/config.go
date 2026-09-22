package metricstore

import (
	"fmt"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

const (
	// DefaultRollupRawRetention documents the fixed in-memory exact-sample
	// window. Samples older than one minute are losslessly byte-encoded;
	// older history is served by the persisted rollup ladder.
	DefaultRollupRawRetention = 10 * time.Minute
	DefaultRollupFinestTier   = time.Minute
	defaultRollupPointLimit   = 600

	defaultRollupMinuteRetentionMinutes     = defaultRollupPointLimit
	defaultRollupFiveMinuteRetentionMinutes = 5 * defaultRollupPointLimit
	defaultRollupHourRetentionHours         = defaultRollupPointLimit
	defaultRollupDayRetentionDays           = 100 * 365
)

// MetricStoreConfig 保存 metric store 配置。
//
// 指标 store 始终使用主 PostgreSQL 连接池；TablePrefix 只负责把指标表与
// 控制面表分开。这里不保存第二个数据库的 driver、DSN 或连接池参数。
type MetricStoreConfig struct {
	TablePrefix string `json:"metric_table_prefix" default:"metric_"` // 表名前缀
	// RollupMinuteRetentionMinutes controls the persisted 1-minute bucket window.
	RollupMinuteRetentionMinutes int `json:"metric_rollup_minute_retention_minutes" default:"600"`
	// RollupFiveMinuteRetentionMinutes controls the persisted 5-minute bucket window.
	RollupFiveMinuteRetentionMinutes int `json:"metric_rollup_five_minute_retention_minutes" default:"3000"`
	// RollupHourRetentionHours controls the persisted 1-hour bucket window.
	RollupHourRetentionHours int `json:"metric_rollup_hour_retention_hours" default:"600"`
}

// MetricStoreConfigKeys 配置键。
const (
	MetricTablePrefixKey                      = "metric_table_prefix"
	MetricRollupMinuteRetentionMinutesKey     = "metric_rollup_minute_retention_minutes"
	MetricRollupFiveMinuteRetentionMinutesKey = "metric_rollup_five_minute_retention_minutes"
	MetricRollupHourRetentionHoursKey         = "metric_rollup_hour_retention_hours"
)

// buildMetricConfig 根据共享 PostgreSQL 连接池构造底层 metric.Config。
// autoMigrate 控制是否在 Open 时自动建表：正式初始化/热加载时为 true，
// 仅做连接测试时为 false（不写入 schema，避免对目标库产生副作用）。
func buildMetricConfig(cfg *MetricStoreConfig, autoMigrate bool) (metric.Config, error) {
	if cfg == nil {
		return metric.Config{}, fmt.Errorf("metric store config is nil")
	}
	tablePrefix := cfg.TablePrefix
	if tablePrefix == "" {
		tablePrefix = "metric_"
	}
	opts := []metric.Option{
		metric.WithTablePrefix(tablePrefix),
		metric.WithAutoMigrate(autoMigrate),
	}
	policy, err := rollupPolicyFromConfig(cfg)
	if err != nil {
		return metric.Config{}, err
	}
	opts = append(opts, metric.WithRollupPolicy(policy))

	db, err := sharedDatabase()
	if err != nil {
		return metric.Config{}, fmt.Errorf("get shared PostgreSQL connection pool: %w", err)
	}
	shared := metric.PostgreSQL("", opts...)
	shared.DB = db
	// dbcore owns and tunes the process-wide pool. Do not overwrite its limits
	// while opening a second Store view over the same *sql.DB.
	shared.MaxOpenConns = 0
	shared.MaxIdleConns = 0
	shared.ConnMaxLifetime = 0
	return shared, nil
}

func defaultRollupPolicy() metric.RollupPolicy {
	return rollupPolicyFromValues(
		defaultRollupMinuteRetentionMinutes,
		defaultRollupFiveMinuteRetentionMinutes,
		defaultRollupHourRetentionHours,
	)
}

func rollupPolicyFromConfig(cfg *MetricStoreConfig) (metric.RollupPolicy, error) {
	if cfg == nil {
		return metric.RollupPolicy{}, fmt.Errorf("metric store config is nil")
	}

	minuteRetention := cfg.RollupMinuteRetentionMinutes
	fiveMinuteRetention := cfg.RollupFiveMinuteRetentionMinutes
	hourRetention := cfg.RollupHourRetentionHours
	// Zero values are treated as omitted so callers can change only one rollup
	// setting without having to repeat the other defaults.
	if minuteRetention == 0 {
		minuteRetention = defaultRollupMinuteRetentionMinutes
	}
	if fiveMinuteRetention == 0 {
		fiveMinuteRetention = defaultRollupFiveMinuteRetentionMinutes
	}
	if hourRetention == 0 {
		hourRetention = defaultRollupHourRetentionHours
	}
	if minuteRetention < 0 || fiveMinuteRetention < 0 || hourRetention < 0 {
		return metric.RollupPolicy{}, fmt.Errorf("metric rollup retention values must be positive integers")
	}

	minuteDuration, err := rollupDuration(minuteRetention, time.Minute)
	if err != nil {
		return metric.RollupPolicy{}, err
	}
	fiveMinuteDuration, err := rollupDuration(fiveMinuteRetention, time.Minute)
	if err != nil {
		return metric.RollupPolicy{}, err
	}
	hourDuration, err := rollupDuration(hourRetention, time.Hour)
	if err != nil {
		return metric.RollupPolicy{}, err
	}

	policy := rollupPolicyFromDurations(minuteDuration, fiveMinuteDuration, hourDuration)
	if err := policy.Validate(); err != nil {
		return metric.RollupPolicy{}, fmt.Errorf("invalid metric rollup retention policy: %w", err)
	}
	return policy, nil
}

func rollupPolicyFromValues(minuteRetentionMinutes, fiveMinuteRetentionMinutes, hourRetentionHours int) metric.RollupPolicy {
	return rollupPolicyFromDurations(
		time.Duration(minuteRetentionMinutes)*time.Minute,
		time.Duration(fiveMinuteRetentionMinutes)*time.Minute,
		time.Duration(hourRetentionHours)*time.Hour,
	)
}

func rollupPolicyFromDurations(minuteRetention, fiveMinuteRetention, hourRetention time.Duration) metric.RollupPolicy {
	return metric.RollupPolicy{
		RawRetention: DefaultRollupRawRetention,
		Tiers: []metric.RollupTier{
			{Interval: time.Minute, Retention: minuteRetention},
			{Interval: 5 * time.Minute, Retention: fiveMinuteRetention},
			{Interval: time.Hour, Retention: hourRetention},
			// Daily buckets form the terminal tier. They remain available until
			// the metric's own retention policy removes them.
			{Interval: 24 * time.Hour, Retention: time.Duration(defaultRollupDayRetentionDays) * 24 * time.Hour},
		},
		Compression: 30,
	}
}

func rollupDuration(value int, unit time.Duration) (time.Duration, error) {
	maxDurationValue := int64((time.Duration(1<<63 - 1)) / unit)
	if value <= 0 || int64(value) > maxDurationValue {
		return 0, fmt.Errorf("metric rollup retention value must be a positive duration")
	}
	return time.Duration(value) * unit, nil
}
