package cmd

import (
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
)

func TestChpasswdDatabaseFileCheckIsSkippedForPostgres(t *testing.T) {
	if got := shouldCheckChpasswdDatabaseFile(flags.DatabaseTypePostgres); got {
		t.Fatal("PostgreSQL chpasswd should not require a SQLite database file")
	}
}

func TestChpasswdDatabaseFileCheckRemainsForSQLite(t *testing.T) {
	if got := shouldCheckChpasswdDatabaseFile(flags.DatabaseTypeSQLite); !got {
		t.Fatal("SQLite chpasswd should require a database file")
	}
}
