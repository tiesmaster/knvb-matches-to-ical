package sportlink_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink/sportlinktest"
)

func TestClientHappyPath(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	c := srv.Client()
	var logs bytes.Buffer
	c.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()

	pools, err := c.TeamCompetitionData(ctx, "T1779014083")
	if err != nil || len(pools) != 2 || pools[0] != 839615 || pools[1] != 845725 {
		t.Fatalf("pools: %v %v", pools, err)
	}
	items, err := c.PoolProgram(ctx, 839615) // served gzip-compressed
	if err != nil || len(items) != 1 || items[0].Match.PublicMatchID != "M707488589" {
		t.Fatalf("pool program: %v %v", items, err)
	}
	fac, err := c.MatchFacility(ctx, "M707488589")
	if err != nil || fac.FacilityName != "Sportpark Neptunus-Schiebroek" {
		t.Fatalf("facility: %+v %v", fac, err)
	}
	if srv.Logins() != 1 {
		t.Errorf("logins = %d, want 1 per run", srv.Logins())
	}
	want := []string{
		"team/TeamCompetitionData?v=2&PublicTeamId=T1779014083",
		"pool/PoolProgram?v=2&PoolId=839615",
		"match/MatchFacility?v=3&PublicMatchId=M707488589",
	}
	if got := srv.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s", strings.Join(got, "\n"))
	}
	for _, secret := range []string{sportlinktest.Creds.Password, sportlinktest.Creds.ClientSecret, srv.Token()} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("logs leak %q:\n%s", secret, logs.String())
		}
	}
}

func TestClientWrongCredentials(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	c := srv.Client()
	c.Creds.Password = "wrong"
	_, err := c.TeamProgram(context.Background(), "T1")
	if !errors.Is(err, sportlink.ErrAuth) || !strings.Contains(err.Error(), "onjuiste inloggegevens") {
		t.Fatalf("want ErrAuth with server message, got %v", err)
	}
	if len(srv.Requests()) != 0 {
		t.Error("no data call may happen without a token")
	}
}

func TestClientReloginOn401(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	c := srv.Client()
	ctx := context.Background()
	if _, err := c.TeamProgram(ctx, "T1"); err != nil {
		t.Fatal(err)
	}
	srv.Invalidate()
	if _, err := c.TeamProgram(ctx, "T1"); err != nil {
		t.Fatalf("should recover by logging in again: %v", err)
	}
	if srv.Logins() != 2 {
		t.Errorf("logins = %d, want 2", srv.Logins())
	}
}

func TestClientReloginOn600(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"http 600": sportlinktest.Status(600, `{}`),
		"body 600": sportlinktest.Status(http.StatusOK, `{"Error":true,"Status":"600","Message":"token invalid"}`),
	} {
		t.Run(name, func(t *testing.T) {
			srv := sportlinktest.NewServer(t)
			srv.Handle("team/TeamProgram", sportlinktest.Sequence(h, sportlinktest.Status(http.StatusOK, `{"ProgramItemMatch": []}`)))
			if _, err := srv.Client().TeamProgram(context.Background(), "T1"); err != nil {
				t.Fatal(err)
			}
			if srv.Logins() != 2 {
				t.Errorf("logins = %d, want 2", srv.Logins())
			}
		})
	}
}

func TestClientPersistentAuthFailure(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamProgram", sportlinktest.Status(http.StatusUnauthorized, `{"Error":true,"Status":"401"}`))
	_, err := srv.Client().TeamProgram(context.Background(), "T1")
	if !errors.Is(err, sportlink.ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	if srv.Logins() != 2 {
		t.Errorf("exactly one re-login expected, got %d logins", srv.Logins())
	}
}

func TestClientRetries(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamProgram", sportlinktest.Sequence(
		sportlinktest.Status(http.StatusServiceUnavailable, `busy`),
		sportlinktest.Status(http.StatusTooManyRequests, `slow down`),
		sportlinktest.Status(http.StatusOK, `{"ProgramItemMatch": []}`),
	))
	c := srv.Client()
	var waits []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if _, err := c.TeamProgram(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != 2*time.Second {
		t.Errorf("backoff waits = %v", waits)
	}
}

func TestClientGivesUpAfterThreeAttempts(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamProgram", sportlinktest.Status(http.StatusBadGateway, `down`))
	_, err := srv.Client().TeamProgram(context.Background(), "T1")
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("got %v", err)
	}
	if n := len(srv.Requests()); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestClientNoRetryOn4xx(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamProgram", sportlinktest.Status(http.StatusNotFound, `{"Error":true,"Status":"404"}`))
	if _, err := srv.Client().TeamProgram(context.Background(), "T1"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v", err)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestClientUnexpectedShape(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamProgram", sportlinktest.Status(http.StatusOK, `{"Something":"else"}`))
	if _, err := srv.Client().TeamProgram(context.Background(), "T1"); !errors.Is(err, sportlink.ErrUnexpectedShape) {
		t.Fatalf("want ErrUnexpectedShape, got %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv.Handle("team/TeamProgram", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	c := srv.Client()
	c.HTTP.Timeout = 50 * time.Millisecond
	start := time.Now()
	if _, err := c.TeamProgram(context.Background(), "T1"); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("timeout not enforced")
	}
}
