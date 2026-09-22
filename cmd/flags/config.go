package flags

import "strings"

const (
	// DatabaseTypePostgres is the only database backend exposed by the
	// application. The legacy SQLite constant remains here temporarily so
	// package-level compatibility tests and old migration helpers can still
	// compile; it is not selectable from the CLI or environment.
	DatabaseTypePostgres = "postgres"

	// DatabaseTypeSQLite is retained for legacy, explicit migration code. New
	// application startup never selects it.
	DatabaseTypeSQLite = "sqlite"
)

var (
	// DatabaseType is fixed to PostgreSQL for application startup. It remains a
	// variable because the old test/migration helpers still exercise their
	// compatibility paths directly.
	DatabaseType = DatabaseTypePostgres
	// DatabaseFile and RedisURL are legacy compatibility variables. They are no
	// longer wired to CLI flags or environment variables and are ignored by the
	// normal runtime.
	DatabaseFile string
	RedisURL     string
	// DatabaseDSN is the single PostgreSQL connection string shared by the main
	// tables and metric tables.
	DatabaseDSN string

	Listen string
)

func NormalizeDatabaseType(databaseType string) string {
	databaseType = strings.ToLower(strings.TrimSpace(databaseType))
	if databaseType == "" {
		return DatabaseTypePostgres
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
	return DatabaseTypePostgres
}
