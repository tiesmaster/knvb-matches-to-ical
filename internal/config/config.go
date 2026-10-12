// Package config loads the tool's configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	SourceDummy = "dummy"
	SourceLive  = "live"

	DefaultTeamID     = "T1779014083"
	DefaultCalName    = "Neptunus-Schiebroek O8-1"
	DefaultDuration   = time.Hour
	DefaultOutDir     = "./out"
	DefaultRetainDays = 180
	// DefaultFeedName is the local file name used in dry-run mode when
	// R2_OBJECT_KEY is not set.
	DefaultFeedName = "calendar.ics"
	stateFileName   = "state.json"
)

// Config is the fully validated runtime configuration.
type Config struct {
	Source string

	KNVBUser         string
	KNVBPass         string
	KNVBClientID     string
	KNVBClientSecret string

	TeamID      string
	OnlyOwnTeam bool
	IncludePast bool

	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Bucket          string
	R2ObjectKey       string

	CalName         string
	DefaultDuration time.Duration
	RetainDays      int
	NotifyURL       string

	DryRun     bool
	OutDir     string
	AllowEmpty bool
	LogLevel   slog.Level
}

// StateKey is the key of state.json, stored next to the feed object.
func (c Config) StateKey() string {
	return path.Join(path.Dir(c.FeedKey()), stateFileName)
}

// FeedKey is the object key (or, in dry-run mode, the relative file path) of the .ics.
func (c Config) FeedKey() string {
	if c.R2ObjectKey == "" {
		return DefaultFeedName
	}
	return c.R2ObjectKey
}

// LogValue implements slog.LogValuer and never includes secrets.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("source", c.Source),
		slog.String("team_id", c.TeamID),
		slog.Bool("only_own_team", c.OnlyOwnTeam),
		slog.Bool("include_past", c.IncludePast),
		slog.String("r2_bucket", c.R2Bucket),
		slog.Bool("dry_run", c.DryRun),
		slog.String("out_dir", c.OutDir),
		slog.Bool("allow_empty", c.AllowEmpty),
		slog.Bool("notify", c.NotifyURL != ""),
		slog.Duration("default_duration", c.DefaultDuration),
		slog.Int("retain_days", c.RetainDays),
	)
}

// Load reads the configuration via getenv (normally os.Getenv) and validates it.
// All problems are reported together.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	str := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	boolean := func(k string) bool {
		v := get(k)
		if v == "" {
			return false
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid boolean %q", k, v))
		}
		return b
	}
	required := func(k string) string {
		v := getenv(k) // credentials are not trimmed
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", k))
		}
		return v
	}

	c := Config{
		Source:      strings.ToLower(str("SOURCE", SourceDummy)),
		TeamID:      str("TEAM_ID", DefaultTeamID),
		OnlyOwnTeam: boolean("ONLY_OWN_TEAM"),
		IncludePast: boolean("INCLUDE_PAST"),
		CalName:     str("CAL_NAME", DefaultCalName),
		NotifyURL:   get("NOTIFY_URL"),
		DryRun:      boolean("DRY_RUN"),
		OutDir:      str("OUT_DIR", DefaultOutDir),
		AllowEmpty:  boolean("ALLOW_EMPTY"),
		R2ObjectKey: strings.TrimPrefix(get("R2_OBJECT_KEY"), "/"),
	}

	c.DefaultDuration = DefaultDuration
	if v := get("DEFAULT_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("DEFAULT_DURATION: invalid positive duration %q (e.g. 1h, 75m)", v))
		}
		c.DefaultDuration = d
	}

	c.RetainDays = DefaultRetainDays
	if v := get("RETAIN_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Errorf("RETAIN_DAYS: invalid non-negative integer %q", v))
		}
		c.RetainDays = n
	}

	if v := get("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL: invalid level %q (debug, info, warn, error)", v))
		}
	}

	switch c.Source {
	case SourceDummy:
	case SourceLive:
		c.KNVBUser = required("KNVB_USER")
		c.KNVBPass = required("KNVB_PASS")
		c.KNVBClientID = required("KNVB_CLIENT_ID")
		c.KNVBClientSecret = required("KNVB_CLIENT_SECRET")
	default:
		errs = append(errs, fmt.Errorf("SOURCE: must be %q or %q, got %q", SourceDummy, SourceLive, c.Source))
	}

	if !c.DryRun {
		c.R2AccountID = required("R2_ACCOUNT_ID")
		c.R2AccessKeyID = required("R2_ACCESS_KEY_ID")
		c.R2SecretAccessKey = required("R2_SECRET_ACCESS_KEY")
		c.R2Bucket = required("R2_BUCKET")
		if c.R2ObjectKey == "" {
			errs = append(errs, errors.New("R2_OBJECT_KEY is required"))
		}
	}
	if c.R2ObjectKey != "" && (strings.HasSuffix(c.R2ObjectKey, "/") || path.Base(c.R2ObjectKey) == stateFileName) {
		errs = append(errs, fmt.Errorf("R2_OBJECT_KEY: %q must name a file other than %s", c.R2ObjectKey, stateFileName))
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return c, nil
}
