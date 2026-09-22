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
	if !configFlagChanged(cmd, "db-dsn") {
		flags.DatabaseDSN = GetEnv("KOMARI_DB_DSN", "")
	}
}

func configFlagChanged(cmd *cobra.Command, name string) bool {
	if cmd != nil && cmd.Flags().Changed(name) {
		return true
	}
	return RootCmd.PersistentFlags().Changed(name)
}

func init() {
	// PostgreSQL is the sole application database. Metrics reuse this same DSN
	// and are separated by table prefix; there is no second database selector.
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseDSN, "db-dsn", "", "PostgreSQL connection DSN shared by application and metric tables [env: KOMARI_DB_DSN]")
	RootCmd.PersistentPreRun = applyEnvironmentDefaults
}
