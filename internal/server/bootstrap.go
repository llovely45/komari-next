package server

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/utils"
)

// Bootstrap initializes the data directory, primary database, and settings.
func (a *App) Bootstrap() error {
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create theme directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin-data", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin storage directory: %w", err)
	}

	dbcore.SetVersionID(utils.CurrentVersion + "-" + utils.VersionHash)
	if err := dbcore.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	a.dbReady = true
	a.addCleanup("database", func(context.Context) error { return dbcore.Close() })
	sqlDB, err := dbcore.GetSQLDB()
	if err != nil {
		return fmt.Errorf("failed to get shared database pool: %w", err)
	}
	metricstore.SetSharedDatabase(sqlDB)
	// Older releases stored an independent metric backend and pool settings.
	// Metrics now live in prefixed tables on this same PostgreSQL connection, so
	// retire those rows before loading settings or exposing the admin API.
	if err := config.DeleteKeys(
		"metric_db_driver",
		"metric_db_dsn",
		"metric_max_open_conns",
		"metric_max_idle_conns",
		"metric_migration_target",
	); err != nil {
		return fmt.Errorf("failed to remove retired metric database settings: %w", err)
	}

	gin.SetMode(gin.ReleaseMode)
	settings, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	a.settings = settings
	return nil
}
