package sportlink

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the production entity host (REQUIREMENTS §2.1).
	DefaultBaseURL = "https://app-vnl-production.sportlink.com"
	entityPrefix   = "/entity/common/memberportal/app/"

	// App identity mirrored in the User-Agent (REQUIREMENTS §5.3).
	appVersion = "6.64.0"
	appBuild   = "2026025447"
	appDevice  = "Pixel 8/Google/34"

	maxAttempts  = 3
	maxBodyBytes = 10 << 20
)

// ErrAuth means the credentials were rejected, or a fresh token was rejected again.
var ErrAuth = errors.New("sportlink authentication failed")

// Credentials for the legacy OAuth2 password grant (REQUIREMENTS §5.2).
type Credentials struct {
	Username     string
	Password     string
	ClientID     string
	ClientSecret string
}

// Client talks to the Sportlink entity API. It is not safe for concurrent use.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Creds   Credentials
	Logger  *slog.Logger
	// Sleep waits between retries; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	token string
}

// NewClient returns a client with sane timeouts.
func NewClient(creds Credentials, logger *slog.Logger) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Creds:   creds,
		Logger:  logger,
		Sleep:   sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Login performs the password grant and stores the access token.
func (c *Client) Login(ctx context.Context) error {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {c.Creds.Username},
		"password":   {c.Creds.Password},
		"client_id":  {c.Creds.ClientID},
		"secret":     {c.Creds.ClientSecret},
	}
	status, body, err := c.doWithRetry(ctx, "login", func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/oauth/token", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.setAppHeaders(req, "")
		return req, nil
	})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(body, &e)
		return fmt.Errorf("%w: login returned HTTP %d: %s %s", ErrAuth, status, e.Error, e.Description)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return fmt.Errorf("%w: login response has no access_token", ErrAuth)
	}
	c.token = tok.AccessToken
	c.log().Info("logged in", "expires_in_s", tok.ExpiresIn)
	return nil
}

// Get fetches an entity sub-path (everything after /entity/common/memberportal/app/).
// navVersion is sent as X-Navajo-Version when > 0. On 401 or the app's "600"
// pseudo-status it logs in again once, then fails with ErrAuth.
func (c *Client) Get(ctx context.Context, subpath string, navVersion int) ([]byte, error) {
	if c.token == "" {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
	endpoint := subpath
	if i := strings.IndexByte(endpoint, '?'); i >= 0 {
		endpoint = endpoint[:i]
	}
	for relogged := false; ; relogged = true {
		status, body, err := c.doWithRetry(ctx, endpoint, func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+entityPrefix+subpath, nil)
			if err != nil {
				return nil, err
			}
			c.setAppHeaders(req, c.token)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+c.token)
			if navVersion > 0 {
				req.Header.Set("X-Navajo-Version", strconv.Itoa(navVersion))
			}
			return req, nil
		})
		if err != nil {
			return nil, err
		}
		if !isAuthFailure(status, body) {
			if status != http.StatusOK {
				return nil, fmt.Errorf("GET %s: HTTP %d: %s", endpoint, status, snippet(body))
			}
			return body, nil
		}
		if relogged {
			return nil, fmt.Errorf("%w: GET %s rejected a fresh token (HTTP %d)", ErrAuth, endpoint, status)
		}
		c.log().Warn("token rejected; logging in again", "endpoint", endpoint, "status", status)
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
}

// isAuthFailure detects HTTP 401, HTTP 600, or a JSON error body with Status
// "401"/"600" (the app's "token invalid" pseudo-status).
func isAuthFailure(status int, body []byte) bool {
	if status == http.StatusUnauthorized || status == 600 {
		return true
	}
	b := bytes.TrimSpace(body)
	if len(b) == 0 || b[0] != '{' || len(b) > 4096 {
		return false
	}
	var e struct {
		Error  bool `json:"Error"`
		Status any  `json:"Status"`
	}
	if json.Unmarshal(b, &e) != nil || !e.Error {
		return false
	}
	s := fmt.Sprint(e.Status)
	return s == "401" || s == "600"
}

// doWithRetry runs the request up to maxAttempts times, retrying transport
// errors, 429 and 5xx with exponential backoff (honouring Retry-After).
func (c *Client) doWithRetry(ctx context.Context, what string, build func() (*http.Request, error)) (int, []byte, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return 0, nil, err
		}
		status, body, retryAfter, err := c.do(req)
		retryable := err != nil || status == http.StatusTooManyRequests || status >= 500 && status != 600
		if !retryable {
			return status, body, nil
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", what, err)
		} else {
			lastErr = fmt.Errorf("%s: HTTP %d: %s", what, status, snippet(body))
		}
		if attempt == maxAttempts || ctx.Err() != nil {
			return 0, nil, fmt.Errorf("giving up after %d attempts: %w", attempt, lastErr)
		}
		wait := time.Duration(1<<(attempt-1)) * time.Second
		if retryAfter > wait && retryAfter <= 30*time.Second {
			wait = retryAfter
		}
		c.log().Warn("request failed; retrying", "what", what, "attempt", attempt, "wait", wait, "err", lastErr)
		if err := c.Sleep(ctx, wait); err != nil {
			return 0, nil, err
		}
	}
}

func (c *Client) do(req *http.Request) (status int, body []byte, retryAfter time.Duration, err error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, 0, err
	}
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	return resp.StatusCode, body, retryAfter, nil
}

// setAppHeaders sets the headers the app sends on every call (REQUIREMENTS
// §5.3). Accept-Encoding is deliberately left to net/http, which then
// transparently decompresses gzip.
func (c *Client) setAppHeaders(req *http.Request, token string) {
	var tokenHash string
	if token != "" {
		sum := md5.Sum([]byte(token))
		tokenHash = hex.EncodeToString(sum[:])
	}
	req.Header.Set("User-Agent", fmt.Sprintf("sportlink-app-voetbalnl/%s-%s android %s %s (%s)", appVersion, appBuild, appDevice, tokenHash, appVersion))
	req.Header.Set("X-Navajo-Instance", "KNVB")
	req.Header.Set("X-Navajo-Locale", "nl")
}

func (c *Client) log() *slog.Logger {
	if c.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Logger
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// TeamCompetitionData returns the pool ids the team plays in.
func (c *Client) TeamCompetitionData(ctx context.Context, teamID string) ([]int64, error) {
	body, err := c.Get(ctx, "team/TeamCompetitionData?v=2&PublicTeamId="+url.QueryEscape(teamID), 2)
	if err != nil {
		return nil, err
	}
	return DecodePoolIDs(body)
}

// TeamProgram returns the team's upcoming programme.
func (c *Client) TeamProgram(ctx context.Context, teamID string) ([]ProgramItemMatch, error) {
	body, err := c.Get(ctx, "team/TeamProgram?v=3&PublicTeamId="+url.QueryEscape(teamID), 3)
	if err != nil {
		return nil, err
	}
	return DecodeProgram(body)
}

// PoolProgram returns a pool's upcoming programme.
func (c *Client) PoolProgram(ctx context.Context, poolID int64) ([]ProgramItemMatch, error) {
	body, err := c.Get(ctx, "pool/PoolProgram?v=2&PoolId="+strconv.FormatInt(poolID, 10), 2)
	if err != nil {
		return nil, err
	}
	return DecodeProgram(body)
}

// MatchFacility returns venue details for a match.
func (c *Client) MatchFacility(ctx context.Context, matchID string) (*MatchFacility, error) {
	body, err := c.Get(ctx, "match/MatchFacility?v=3&PublicMatchId="+url.QueryEscape(matchID), 3)
	if err != nil {
		return nil, err
	}
	return DecodeMatchFacility(body)
}

// TeamLatestResults returns the team's recently played matches.
func (c *Client) TeamLatestResults(ctx context.Context, teamID string) ([]Match, error) {
	body, err := c.Get(ctx, "team/TeamLatestResults?PublicTeamId="+url.QueryEscape(teamID), 0)
	if err != nil {
		return nil, err
	}
	return DecodeLatestResults(body)
}

// NoSleep is a Sleep that returns immediately (for tests).
func NoSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }
