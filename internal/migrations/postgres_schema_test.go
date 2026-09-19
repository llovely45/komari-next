package migrations

import (
	"strings"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestPostgresDDLDoesNotUseLongtext(t *testing.T) {
	db, err := gorm.Open(postgres.Open("postgres://user:password@localhost:5432/komari?sslmode=disable"), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
		Logger:               gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open PostgreSQL dry-run database: %v", err)
	}

	startupModels := []any{
		&legacyModelConfig{},
		&ClientInfo{},
		&models.User{},
		&models.Client{},
		&models.Log{},
		&models.Clipboard{},
		&models.LoadNotification{},
		&models.OfflineNotification{},
		&models.TrafficReportNotification{},
		&models.PingTask{},
		&models.OidcProvider{},
		&models.MessageSenderProvider{},
		&models.ThemeConfiguration{},
		&models.PluginConfiguration{},
		&models.Session{},
		&models.Task{},
		&models.TaskResult{},
	}

	for _, model := range startupModels {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			t.Fatalf("parse %T: %v", model, err)
		}
		for _, field := range statement.Schema.Fields {
			ddl := strings.ToLower(db.Migrator().FullDataTypeOf(field).SQL)
			if strings.Contains(ddl, "longtext") {
				t.Errorf("PostgreSQL DDL for %T.%s contains unsupported longtext: %s", model, field.Name, ddl)
			}
		}
	}
}
