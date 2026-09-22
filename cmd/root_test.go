package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
)

const rootFlagSmokeEnv = "KOMARI_ROOT_FLAG_SMOKE"

func TestSecretBearingFlagDefaultsDoNotExposeEnvironment(t *testing.T) {
	if os.Getenv(rootFlagSmokeEnv) == "1" {
		for _, name := range []string{"db-dsn"} {
			flag := RootCmd.PersistentFlags().Lookup(name)
			if flag == nil {
				t.Fatalf("flag %q is not registered", name)
			}
			if flag.DefValue != "" {
				t.Fatalf("flag %q has a non-empty default", name)
			}
		}
		if RootCmd.PersistentFlags().Lookup("redis-url") != nil {
			t.Fatal("redis-url must not be registered")
		}
		if RootCmd.PersistentFlags().Lookup("db-type") != nil || RootCmd.PersistentFlags().Lookup("database") != nil {
			t.Fatal("legacy database selector flags must not be registered")
		}

		help := RootCmd.UsageString()
		for _, secret := range []string{"redis-fix-round-secret", "postgres-fix-round-secret"} {
			if strings.Contains(help, secret) {
				t.Fatalf("help output contains a configured secret")
			}
		}
		return
	}

	child := exec.Command(os.Args[0], "-test.run", "^TestSecretBearingFlagDefaultsDoNotExposeEnvironment$", "-test.v")
	child.Env = withoutEnv(os.Environ(), rootFlagSmokeEnv, "KOMARI_REDIS_URL", "KOMARI_DB_DSN")
	child.Env = append(child.Env,
		rootFlagSmokeEnv+"=1",
		"KOMARI_REDIS_URL=redis://:redis-fix-round-secret@127.0.0.1:6379/0",
		"KOMARI_DB_DSN=postgres://user:postgres-fix-round-secret@127.0.0.1:5432/komari",
	)
	if err := child.Run(); err != nil {
		t.Fatalf("secret-bearing flag smoke test failed")
	}
}

func TestRootPersistentPreRunUsesEnvironmentWhenFlagsUnchanged(t *testing.T) {
	preserveRootConfigState(t)
	t.Setenv("KOMARI_DB_DSN", "postgres://env-user:env-password@db.example/komari")

	flags.DatabaseDSN = ""
	for _, name := range []string{"db-dsn"} {
		RootCmd.PersistentFlags().Lookup(name).Changed = false
	}

	if RootCmd.PersistentPreRun == nil {
		t.Fatal("RootCmd.PersistentPreRun is not configured")
	}
	RootCmd.PersistentPreRun(RootCmd, nil)

	if flags.DatabaseType != flags.DatabaseTypePostgres || flags.DatabaseDSN == "" {
		t.Fatalf("environment values were not applied for unchanged flags: type=%q dsn=%q", flags.DatabaseType, flags.DatabaseDSN)
	}
}

func TestRootPersistentPreRunPreservesExplicitCLIValues(t *testing.T) {
	preserveRootConfigState(t)
	t.Setenv("KOMARI_DB_DSN", "postgres://env-user:env-password@db.example/komari")

	if err := RootCmd.PersistentFlags().Set("db-dsn", "postgres://cli-user:cli-password@localhost/komari"); err != nil {
		t.Fatalf("set db-dsn flag: %v", err)
	}

	if RootCmd.PersistentPreRun == nil {
		t.Fatal("RootCmd.PersistentPreRun is not configured")
	}
	RootCmd.PersistentPreRun(RootCmd, nil)

	if flags.DatabaseType != flags.DatabaseTypePostgres || !strings.Contains(flags.DatabaseDSN, "cli-user") {
		t.Fatal("environment values overrode explicitly changed CLI flags")
	}
}

func preserveRootConfigState(t *testing.T) {
	t.Helper()
	previousDSN := flags.DatabaseDSN
	previousValues := make(map[string]string, 1)
	previousChanged := make(map[string]bool, 1)
	for _, name := range []string{"db-dsn"} {
		flag := RootCmd.PersistentFlags().Lookup(name)
		previousValues[name] = flag.Value.String()
		previousChanged[name] = flag.Changed
	}
	t.Cleanup(func() {
		flags.DatabaseType = flags.DatabaseTypePostgres
		flags.DatabaseDSN = previousDSN
		for name, value := range previousValues {
			_ = RootCmd.PersistentFlags().Set(name, value)
			RootCmd.PersistentFlags().Lookup(name).Changed = previousChanged[name]
		}
	})
}

func withoutEnv(environment []string, keys ...string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		keep := true
		for _, key := range keys {
			if strings.HasPrefix(entry, key+"=") {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
