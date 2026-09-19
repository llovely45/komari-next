package cmd

import (
	"fmt"
	"os"

	"github.com/komari-monitor/komari/cmd/flags"

	"github.com/spf13/cobra"
)

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

var RootCmd = &cobra.Command{
	Use:   "Komari",
	Short: "Komari is a simple server monitoring tool",
	Long: `Komari is a simple server monitoring tool.
Made by Akizon77 with love.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.SetArgs([]string{"server"})
		cmd.Execute()
	},
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// applyEnvironmentDefaults resolves environment configuration after Cobra has
// parsed the command line. Secret-bearing values therefore never become
// pflag DefValue strings and cannot appear in help output.
func applyEnvironmentDefaults(cmd *cobra.Command, _ []string) {
	if !configFlagChanged(cmd, "db-type") {
		flags.DatabaseType = GetEnv("KOMARI_DB_TYPE", flags.DatabaseTypeSQLite)
	}
	if !configFlagChanged(cmd, "db-dsn") {
		flags.DatabaseDSN = GetEnv("KOMARI_DB_DSN", "")
	}
	if !configFlagChanged(cmd, "redis-url") {
		flags.RedisURL = GetEnv("KOMARI_REDIS_URL", "")
	}
}

func configFlagChanged(cmd *cobra.Command, name string) bool {
	if cmd != nil && cmd.Flags().Changed(name) {
		return true
	}
	return RootCmd.PersistentFlags().Changed(name)
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseType, "db-type", "t", flags.DatabaseTypeSQLite, "Database type (sqlite or postgres) [env: KOMARI_DB_TYPE]")
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseFile, "database", "d", "./data/komari.db", "SQLite database file path")
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseDSN, "db-dsn", "", "PostgreSQL connection DSN [env: KOMARI_DB_DSN]")
	RootCmd.PersistentFlags().StringVar(&flags.RedisURL, "redis-url", "", "Redis URL for the optional cache layer [env: KOMARI_REDIS_URL]")
	RootCmd.PersistentPreRun = applyEnvironmentDefaults
}
