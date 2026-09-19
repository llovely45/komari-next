package admin

import (
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
)

func TestDatabaseBackupSupportRejectsPostgresUntilDumpRestoreExists(t *testing.T) {
	if supportsDatabaseDownloadBackup(flags.DatabaseTypePostgres) {
		t.Fatal("PostgreSQL backup must not be reported as supported without a dump/restore implementation")
	}
}

func TestDatabaseBackupSupportKeepsSQLite(t *testing.T) {
	if !supportsDatabaseDownloadBackup(flags.DatabaseTypeSQLite) {
		t.Fatal("SQLite backup should remain supported")
	}
}
