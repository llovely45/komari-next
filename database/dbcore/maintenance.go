package dbcore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/komari-monitor/komari/cmd/flags"
)

var maintenanceMu sync.Mutex

// StorageSize returns the physical size of the active main database. PostgreSQL
// is the supported application backend; the SQLite branch remains only for
// legacy compatibility helpers.
func StorageSize() (int64, error) {
	if flags.IsPostgres() {
		db, err := GetSQLDB()
		if err != nil {
			return 0, fmt.Errorf("get main database connection: %w", err)
		}
		var size int64
		if err := db.QueryRow("SELECT pg_database_size(current_database())").Scan(&size); err != nil {
			return 0, fmt.Errorf("query PostgreSQL database size: %w", err)
		}
		return size, nil
	}
	if !flags.IsSQLite() {
		return 0, errors.New("unsupported main database backend")
	}
	return sqliteFileSetSize(resolveDatabaseFile())
}

// ReclaimSpace runs the backend-specific physical maintenance operation.
func ReclaimSpace(ctx context.Context) error {
	if flags.IsPostgres() {
		maintenanceMu.Lock()
		defer maintenanceMu.Unlock()
		db, err := GetSQLDB()
		if err != nil {
			return fmt.Errorf("get main database connection: %w", err)
		}
		if _, err := db.ExecContext(ctx, "VACUUM (FULL, ANALYZE)"); err != nil {
			return fmt.Errorf("vacuum PostgreSQL main database: %w", err)
		}
		return nil
	}
	if !flags.IsSQLite() {
		return errors.New("unsupported main database backend")
	}

	maintenanceMu.Lock()
	defer maintenanceMu.Unlock()

	db, err := GetDBInstance().DB()
	if err != nil {
		return fmt.Errorf("get main database connection: %w", err)
	}
	if err := checkpointSQLiteWAL(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum main database: %w", err)
	}
	return checkpointSQLiteWAL(ctx, db)
}

func checkpointSQLiteWAL(ctx context.Context, db *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("checkpoint main database WAL: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf(
			"checkpoint main database WAL: database is busy (%d log frames, %d checkpointed)",
			logFrames,
			checkpointedFrames,
		)
	}
	return nil
}

func sqliteFileSetSize(dsn string) (int64, error) {
	path := strings.TrimPrefix(strings.TrimSpace(dsn), "file:")
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	if path == "" || path == ":memory:" || strings.Contains(strings.ToLower(dsn), "mode=memory") {
		return 0, errors.New("database is not backed by a local file")
	}

	var total int64
	foundDatabase := false
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		switch {
		case err == nil:
			total += info.Size()
			if suffix == "" {
				foundDatabase = true
			}
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return 0, fmt.Errorf("stat database file %q: %w", path+suffix, err)
		}
	}
	if !foundDatabase {
		return 0, fmt.Errorf("database file %q does not exist", path)
	}
	return total, nil
}
