package flags

import "strings"

const (
	DatabaseTypeSQLite   = "sqlite"
	DatabaseTypePostgres = "postgres"
)

var (
	// 数据库配置
	DatabaseType string // 数据库类型：sqlite 或 postgres
	DatabaseFile string // SQLite 数据库文件路径；兼容旧版 --database 参数
	DatabaseDSN  string // PostgreSQL 连接串
	RedisURL     string // Redis 连接 URL；为空时禁用缓存

	Listen string
)

func NormalizeDatabaseType(databaseType string) string {
	databaseType = strings.ToLower(strings.TrimSpace(databaseType))
	if databaseType == "" {
		return DatabaseTypeSQLite
	}
	if databaseType == "postgresql" {
		return DatabaseTypePostgres
	}
	return databaseType
}

func ApplyDatabaseTypeNormalization() string {
	DatabaseType = NormalizeDatabaseType(DatabaseType)
	return DatabaseType
}

func IsSQLite() bool {
	return NormalizeDatabaseType(DatabaseType) == DatabaseTypeSQLite
}

func IsPostgres() bool {
	return NormalizeDatabaseType(DatabaseType) == DatabaseTypePostgres
}

func SupportedDatabaseTypes() string {
	return DatabaseTypeSQLite + ", " + DatabaseTypePostgres
}
