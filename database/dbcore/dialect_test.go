package dbcore

import (
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
)

func TestDatabaseDialectorSelectsPostgresWithoutConnecting(t *testing.T) {
	dialector, err := databaseDialector(flags.DatabaseTypePostgres, "postgres://user:password@localhost:5432/komari?sslmode=disable")
	if err != nil {
		t.Fatalf("databaseDialector: %v", err)
	}
	if got := dialector.Name(); got != "postgres" {
		t.Fatalf("dialector name = %q, want postgres", got)
	}
}

func TestDatabaseDialectorSelectsSQLite(t *testing.T) {
	dialector, err := databaseDialector(flags.DatabaseTypeSQLite, "./data/test.db")
	if err != nil {
		t.Fatalf("databaseDialector: %v", err)
	}
	if got := dialector.Name(); got != "sqlite" {
		t.Fatalf("dialector name = %q, want sqlite", got)
	}
}

func TestDatabaseDialectorRejectsUnsupportedType(t *testing.T) {
	if _, err := databaseDialector("mysql", "mysql://localhost/komari"); err == nil {
		t.Fatal("databaseDialector unexpectedly accepted mysql")
	}
}

func TestDatabaseDSNDoesNotFallbackToSQLiteDatabaseFile(t *testing.T) {
	previousDSN := flags.DatabaseDSN
	previousFile := flags.DatabaseFile
	t.Cleanup(func() {
		flags.DatabaseDSN = previousDSN
		flags.DatabaseFile = previousFile
	})

	flags.DatabaseDSN = ""
	flags.DatabaseFile = "/var/lib/komari/komari.db"

	if got := databaseDSN(); got != "" {
		t.Fatalf("databaseDSN() = %q, want empty DSN when only SQLite database file is set", got)
	}
}

func TestBackupOnVersionUpgradeUsesDatabaseTypeSnapshot(t *testing.T) {
	previousVersion := versionID
	previousType := flags.DatabaseType
	t.Cleanup(func() {
		versionID = previousVersion
		flags.DatabaseType = previousType
	})

	versionID = "test-version"
	flags.DatabaseType = flags.DatabaseTypeSQLite

	backupOnVersionUpgrade(flags.DatabaseTypePostgres)
}
