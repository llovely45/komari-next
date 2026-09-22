package flags

import "testing"

func TestNormalizeDatabaseType(t *testing.T) {
	tests := map[string]string{
		"":            DatabaseTypePostgres,
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

func TestSupportedDatabaseTypesOnlyIncludesPostgres(t *testing.T) {
	if got := SupportedDatabaseTypes(); got != DatabaseTypePostgres {
		t.Fatalf("SupportedDatabaseTypes() = %q, want %q", got, DatabaseTypePostgres)
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
