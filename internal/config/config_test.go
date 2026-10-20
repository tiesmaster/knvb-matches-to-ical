package config

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaultsDummyDryRun(t *testing.T) {
	c, err := Load(env(map[string]string{"DRY_RUN": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != SourceDummy || c.TeamID != DefaultTeamID || c.DefaultDuration != time.Hour ||
		c.OutDir != DefaultOutDir || c.RetainDays != DefaultRetainDays || !c.DryRun || !c.OnlyOwnTeam {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.FeedKey() != "calendar.ics" || c.StateKey() != "state.json" {
		t.Errorf("keys: feed=%q state=%q", c.FeedKey(), c.StateKey())
	}
}

func TestOnlyOwnTeamCanBeDisabled(t *testing.T) {
	c, err := Load(env(map[string]string{"DRY_RUN": "1", "ONLY_OWN_TEAM": "false"}))
	if err != nil || c.OnlyOwnTeam {
		t.Errorf("ONLY_OWN_TEAM=false: %v %v", c.OnlyOwnTeam, err)
	}
}

func TestLoadLiveRequiresCredentials(t *testing.T) {
	_, err := Load(env(map[string]string{"SOURCE": "live", "DRY_RUN": "true", "KNVB_USER": "u"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, k := range []string{"KNVB_PASS", "KNVB_CLIENT_ID", "KNVB_CLIENT_SECRET"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error should mention %s: %v", k, err)
		}
	}
	if strings.Contains(err.Error(), "KNVB_USER") {
		t.Errorf("KNVB_USER was set: %v", err)
	}
}

func TestLoadPublishRequiresR2(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, k := range []string{"R2_ACCOUNT_ID", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_BUCKET", "R2_OBJECT_KEY"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error should mention %s: %v", k, err)
		}
	}
}

func TestLoadFull(t *testing.T) {
	_, err := Load(env(map[string]string{
		"SOURCE": "live", "KNVB_USER": "u", "KNVB_PASS": "p", "KNVB_CLIENT_ID": "id", "KNVB_CLIENT_SECRET": "s",
		"R2_ACCOUNT_ID": "acc", "R2_ACCESS_KEY_ID": "ak", "R2_SECRET_ACCESS_KEY": "sk", "R2_BUCKET": "b",
		"R2_OBJECT_KEY": "/x7f3k/o8-1.ics", "DEFAULT_DURATION": "75m", "ONLY_OWN_TEAM": "yes",
	}))
	if err == nil {
		t.Fatal("expected error for invalid boolean 'yes'")
	}
	c, err := Load(env(map[string]string{
		"SOURCE": "LIVE", "KNVB_USER": "u", "KNVB_PASS": "p", "KNVB_CLIENT_ID": "id", "KNVB_CLIENT_SECRET": "s",
		"R2_ACCOUNT_ID": "acc", "R2_ACCESS_KEY_ID": "ak", "R2_SECRET_ACCESS_KEY": "sk", "R2_BUCKET": "b",
		"R2_OBJECT_KEY": "/x7f3k/o8-1.ics", "DEFAULT_DURATION": "75m", "ONLY_OWN_TEAM": "1", "LOG_LEVEL": "debug",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.FeedKey() != "x7f3k/o8-1.ics" || c.StateKey() != "x7f3k/state.json" {
		t.Errorf("keys: feed=%q state=%q", c.FeedKey(), c.StateKey())
	}
	if c.DefaultDuration != 75*time.Minute || !c.OnlyOwnTeam || c.LogLevel != slog.LevelDebug {
		t.Errorf("unexpected: %+v", c)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	for _, m := range []map[string]string{
		{"DRY_RUN": "1", "SOURCE": "csv"},
		{"DRY_RUN": "1", "DEFAULT_DURATION": "-5m"},
		{"DRY_RUN": "1", "DEFAULT_DURATION": "60"},
		{"DRY_RUN": "1", "RETAIN_DAYS": "x"},
		{"DRY_RUN": "1", "R2_OBJECT_KEY": "abc/state.json"},
		{"DRY_RUN": "1", "LOG_LEVEL": "loud"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("expected error for %v", m)
		}
	}
}

func TestLogValueHasNoSecrets(t *testing.T) {
	c := Config{KNVBUser: "user@example.org", KNVBPass: "hunter2", KNVBClientSecret: "topsecret", R2SecretAccessKey: "r2secret"}
	s := fmt.Sprint(c.LogValue())
	for _, secret := range []string{"user@example.org", "hunter2", "topsecret", "r2secret"} {
		if strings.Contains(s, secret) {
			t.Errorf("LogValue leaks %q: %s", secret, s)
		}
	}
}
