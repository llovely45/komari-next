package flags

import "testing"

func TestNormalizeDatabaseType(t *testing.T) {
	tests := map[string]string{
		"":            DatabaseTypeSQLite,
		"sqlite":      DatabaseTypeSQLite,
		" SQLite ":    DatabaseTypeSQLite,
		"SQLITE":      DatabaseTypeSQLite,
		"postgres":    DatabaseTypePostgres,
		"postgresql":  DatabaseTypePostgres,
		" PostgreSQL": DatabaseTypePostgres,
	}

	for input, want := range tests {
		if got := NormalizeDatabaseType(input); got != want {
			t.Fatalf("NormalizeDatabaseType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSupportedDatabaseTypesIncludesPostgres(t *testing.T) {
	if got := SupportedDatabaseTypes(); got != "sqlite, postgres" {
		t.Fatalf("SupportedDatabaseTypes() = %q, want %q", got, "sqlite, postgres")
	}
}

func TestIsPostgres(t *testing.T) {
	previous := DatabaseType
	t.Cleanup(func() { DatabaseType = previous })

	DatabaseType = "postgresql"
	if !IsPostgres() {
		t.Fatal("IsPostgres() = false for postgresql")
	}
	DatabaseType = "sqlite"
	if IsPostgres() {
		t.Fatal("IsPostgres() = true for sqlite")
	}
}
