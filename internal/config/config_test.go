package config

import (
	"FriendlyCardFinder/env"
	"path/filepath"
	"strings"
	"testing"
)

// setEnv applies a set of variables for one test, clearing every other variable
// Load reads so a leaked value from the developer's shell cannot make a test
// pass. t.Setenv restores the previous values afterwards.
func setEnv(t *testing.T, vars map[env.EnvKey]string) {
	t.Helper()
	all := []env.EnvKey{
		env.BotToken, env.StoragePath, env.Env,
		env.DeckboxLogin, env.DeckboxPassword, env.DeckboxSessionCookie,
		env.FreshnessTimeLimitHours, env.CardListRefreshHours, env.CardListBatchSize,
		env.AdminAddr, env.AdminUser, env.AdminPassword,
	}
	for _, k := range all {
		t.Setenv(string(k), vars[k])
	}
}

// validEnv is the smallest environment the bot may start with.
func validEnv() map[env.EnvKey]string {
	return map[env.EnvKey]string{
		env.BotToken:        "token",
		env.StoragePath:     "/data/storage.db",
		env.DeckboxLogin:    "user",
		env.DeckboxPassword: "pass",
	}
}

func TestLoadRejectsAnUnstartableConfiguration(t *testing.T) {
	// Each of these leaves the bot unable to do its job, so it must fail at boot
	// with a readable message rather than somewhere further in.
	tests := []struct {
		name    string
		mutate  func(map[env.EnvKey]string)
		wantErr string
	}{
		{
			name:    "no storage path",
			mutate:  func(m map[env.EnvKey]string) { delete(m, env.StoragePath) },
			wantErr: "STORAGE_PATH",
		},
		{
			name:    "no bot token",
			mutate:  func(m map[env.EnvKey]string) { delete(m, env.BotToken) },
			wantErr: "BOT_TOKEN",
		},
		{
			name: "no deckbox auth at all",
			mutate: func(m map[env.EnvKey]string) {
				delete(m, env.DeckboxLogin)
				delete(m, env.DeckboxPassword)
			},
			wantErr: "deckbox auth not configured",
		},
		{
			name:    "half a credential is not a credential",
			mutate:  func(m map[env.EnvKey]string) { delete(m, env.DeckboxPassword) },
			wantErr: "deckbox auth not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := validEnv()
			tt.mutate(vars)
			setEnv(t, vars)

			cfg, err := Load()
			if err == nil {
				t.Fatalf("expected an error, got config %+v", cfg)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadAcceptsACookieInsteadOfCredentials(t *testing.T) {
	// DECKBOX_SESSION_COOKIE is the escape hatch for when logging in is not an
	// option; it stands in for both credentials.
	vars := validEnv()
	delete(vars, env.DeckboxLogin)
	delete(vars, env.DeckboxPassword)
	vars[env.DeckboxSessionCookie] = "manual-cookie"
	setEnv(t, vars)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("a session cookie should be enough on its own: %v", err)
	}
	if cfg.DeckboxSessionCookie != "manual-cookie" {
		t.Errorf("DeckboxSessionCookie = %q, want it carried through", cfg.DeckboxSessionCookie)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if cfg.FreshnessTimeLimitHours != 24 {
		t.Errorf("FreshnessTimeLimitHours = %d, want 24", cfg.FreshnessTimeLimitHours)
	}
	if cfg.CardListRefreshHours != 3 {
		t.Errorf("CardListRefreshHours = %d, want 3", cfg.CardListRefreshHours)
	}
	if cfg.AdminAddr != defaultAdminAddr {
		t.Errorf("AdminAddr = %q, want %q", cfg.AdminAddr, defaultAdminAddr)
	}
	// Zero means "storage picks", so the default lives in exactly one place.
	if cfg.CardListBatchSize != 0 {
		t.Errorf("CardListBatchSize = %d, want 0 so sqlite.New applies its default", cfg.CardListBatchSize)
	}
	// An empty admin password is not an error here — the panel refuses to start
	// on its own, which is what keeps the bot running without one.
	if cfg.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty", cfg.AdminPassword)
	}
}

func TestLoadDerivesTheCookiePathFromStorage(t *testing.T) {
	// The session cookie is a secret that belongs beside the database, on the
	// same mounted volume — not in the container's working directory.
	vars := validEnv()
	vars[env.StoragePath] = filepath.Join("/data", "nested", "storage.db")
	setEnv(t, vars)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	want := filepath.Join("/data", "nested", "deckbox_session")
	if cfg.DeckboxCookiePath != want {
		t.Errorf("DeckboxCookiePath = %q, want %q", cfg.DeckboxCookiePath, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	vars := validEnv()
	vars[env.FreshnessTimeLimitHours] = "48"
	vars[env.CardListRefreshHours] = "not a number" // falls back rather than failing
	vars[env.CardListBatchSize] = "2500"
	vars[env.AdminAddr] = ":9090"
	setEnv(t, vars)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if cfg.FreshnessTimeLimitHours != 48 {
		t.Errorf("FreshnessTimeLimitHours = %d, want the override", cfg.FreshnessTimeLimitHours)
	}
	if cfg.CardListRefreshHours != 3 {
		t.Errorf("CardListRefreshHours = %d, want the default when the value is unparsable", cfg.CardListRefreshHours)
	}
	if cfg.CardListBatchSize != 2500 {
		t.Errorf("CardListBatchSize = %d, want the override", cfg.CardListBatchSize)
	}
	if cfg.AdminAddr != ":9090" {
		t.Errorf("AdminAddr = %q, want the override", cfg.AdminAddr)
	}
}
