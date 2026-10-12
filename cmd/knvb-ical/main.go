// Command knvb-ical fetches KNVB youth-team fixtures from the Sportlink backend,
// renders them as an iCalendar feed and publishes it to Cloudflare R2.
// It runs to completion and exits; non-zero means nothing new was published.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // distroless has no zoneinfo

	"github.com/tiesmaster/knvb-matches-to-ical/internal/config"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/notify"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/run"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/source"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/store"
)

const runTimeout = 5 * time.Minute

func main() {
	os.Exit(realMain())
}

func realMain() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("configuration error", "err", err)
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("starting", "config", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var src source.FixtureSource
	switch cfg.Source {
	case config.SourceLive:
		client := sportlink.NewClient(sportlink.Credentials{
			Username:     cfg.KNVBUser,
			Password:     cfg.KNVBPass,
			ClientID:     cfg.KNVBClientID,
			ClientSecret: cfg.KNVBClientSecret,
		}, logger)
		// Fresh login every run; the 1 h token outlives the run.
		if err := client.Login(ctx); err != nil {
			logger.Error("login failed", "err", err)
			return 1
		}
		src = &source.Live{API: client, TeamID: cfg.TeamID, IncludePast: cfg.IncludePast, Logger: logger}
	default:
		src = source.Dummy{Reschedule: cfg.DummyReschedule}
	}

	var st store.Store
	if cfg.DryRun {
		st = store.Local{Dir: cfg.OutDir}
	} else {
		st = store.NewR2(store.R2Config{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket:          cfg.R2Bucket,
		})
	}

	r := &run.Runner{
		Source: src,
		Store:  st,
		Now:    time.Now,
		Logger: logger,
		Options: run.Options{
			FeedKey:         cfg.FeedKey(),
			StateKey:        cfg.StateKey(),
			CalName:         cfg.CalName,
			TeamID:          cfg.TeamID,
			OnlyOwnTeam:     cfg.OnlyOwnTeam,
			DefaultDuration: cfg.DefaultDuration,
			Retain:          time.Duration(cfg.RetainDays) * 24 * time.Hour,
			AllowEmpty:      cfg.AllowEmpty,
		},
	}
	if cfg.NotifyURL != "" {
		r.Notifier = notify.New(cfg.NotifyURL, cfg.CalName, logger)
	}

	res, err := r.Run(ctx)
	if cfg.DryRun {
		printSummary(cfg, res)
	}
	if err != nil {
		logger.Error("run failed; existing feed left untouched", "err", err)
		return 1
	}
	logger.Info("done", "published", res.Published, "events", res.Events, "fetched", res.Fetched, "changes", len(res.Changes))
	return 0
}

func printSummary(cfg config.Config, res run.Result) {
	local := store.Local{Dir: cfg.OutDir}
	fmt.Printf("dry run: %d fetched, %d in feed, %d change(s)\n", res.Fetched, res.Events, len(res.Changes))
	for _, c := range res.Changes {
		fmt.Printf("  %s\n", c)
	}
	if res.Published {
		fmt.Printf("wrote %s\n      %s\n", local.Path(cfg.FeedKey()), local.Path(cfg.StateKey()))
	} else {
		fmt.Println("nothing written")
	}
}
