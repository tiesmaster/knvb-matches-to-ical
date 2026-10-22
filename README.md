# knvb-matches-to-ical

A small headless job that fetches the match programme of a KNVB youth team
(default: **Neptunus-Schiebroek O8-1**, `T1779014083`) from the Sportlink
backend behind the Voetbal.nl app, renders it as an iCalendar feed and uploads
it to a Cloudflare R2 bucket served on a custom domain. Google Calendar (or any
other client) subscribes to that URL.

It runs as a Kubernetes CronJob, runs to completion and exits. Nothing is
exposed: no HTTP server, no ingress.

The API contract (endpoints, auth, headers, field mapping, the Zwaluwen/pool
nuance) is documented in [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md).

## How it works

Each run:

1. **Login**: fresh OAuth2 password grant (the 1 h token outlives the run).
2. **Fetch**: `TeamCompetitionData` → pool ids → `PoolProgram` per pool, merged
   with `TeamProgram` (covers non-pool teams and friendlies) and de-duplicated by
   match id; optionally `TeamLatestResults` (`INCLUDE_PAST`). Then
   `MatchFacility` per match for the address, field, assembly time and dressing
   rooms.
3. **Map** each match to a VEVENT: `UID = <PublicMatchId>@voetbal.nl`,
   DTSTART/DTEND in UTC. SUMMARY names only the opponent: `THUIS: BVCB O8-3`
   or `UIT: VOC O8-5`. Other teams' pool matches (with `ONLY_OWN_TEAM=false`)
   keep `Home – Away`.
4. **Diff** against `state.json` (stored next to the feed). A changed content
   hash bumps `SEQUENCE` and `LAST-MODIFIED`.
5. **Publish**: if nothing changed, nothing is uploaded. Otherwise the `.ics` is
   uploaded first, then `state.json`.
6. **Notify** (optional): an ntfy message when a match in the next 48 h changes
   time, location or status.

### Event lifecycle

| Situation | Feed |
|---|---|
| New match | Added, `SEQUENCE:0` |
| Time/location/details changed | `SEQUENCE` +1 |
| `Status` = Afgelast / Gestaakt / Cancelled / Abandoned | `STATUS:CANCELLED`, summary suffix `(afgelast)` / `(gestaakt)`; kept in the feed |
| `Status` = Uitgesteld / Verplaatst / Postponed | `STATUS:TENTATIVE`, summary suffix `(uitgesteld)` |
| Unknown status string | Treated as scheduled; logged at `WARN` |
| Played match drops out of the programme | Kept unchanged (the API only lists upcoming matches) |
| Upcoming match disappears without a cancellation | Kept as `STATUS:CANCELLED`, summary suffix `(vervallen)` |
| Not returned by the API for `RETAIN_DAYS` after its end | Removed |

Status strings are free-form on the server and matched case-insensitively. The
only value seen live so far is `SCHEDULED`.

### Safety rails

- Any fetch, decode or state error exits non-zero and **leaves the published
  feed untouched**, so the CronJob failure is visible.
- A response without the top-level `ProgramItemMatch` array, or with id-less
  matches, is rejected rather than rendered as an empty calendar.
- If a run yields zero events while the previous state had upcoming matches,
  it refuses to publish unless `ALLOW_EMPTY=1`.
- HTTP timeouts on every call. 429/5xx are retried up to 3 attempts with
  backoff. 401 or the app's `600` pseudo-status trigger one re-login, then the
  run fails.
- Credentials and tokens are never logged.

## Configuration

Environment variables only. Missing or invalid values fail fast, with every
problem listed in one error.

| Variable | Default | Description |
|---|---|---|
| `SOURCE` | `dummy` | `dummy` (embedded fixture plus synthetic matches) or `live` |
| `KNVB_USER` | (none) | KNVB account login. **Required for `live`** |
| `KNVB_PASS` | (none) | KNVB account password. **Required for `live`** |
| `KNVB_CLIENT_ID` | (none) | App OAuth client id (REQUIREMENTS §5.2). **Required for `live`** |
| `KNVB_CLIENT_SECRET` | (none) | App OAuth client secret. **Required for `live`** |
| `TEAM_ID` | `T1779014083` | `PublicTeamId` of the team |
| `ONLY_OWN_TEAM` | `true` | Keep only matches where home or away is `TEAM_ID`. For Zwaluwen teams, both PoolProgram and TeamProgram return every match in the pool (28 matches, 7 of them O8-1's, in the 2e fase). Set `false` to publish the whole pool. Decide before the first publish: switching later turns the dropped matches into `(vervallen)` cancellations |
| `INCLUDE_PAST` | `false` | Also fetch `TeamLatestResults` |
| `R2_ACCOUNT_ID` | (none) | Cloudflare account id. **Required unless `DRY_RUN`** |
| `R2_ACCESS_KEY_ID` | (none) | R2 API token access key. **Required unless `DRY_RUN`** |
| `R2_SECRET_ACCESS_KEY` | (none) | R2 API token secret. **Required unless `DRY_RUN`** |
| `R2_BUCKET` | (none) | Bucket name. **Required unless `DRY_RUN`** |
| `R2_OBJECT_KEY` | (none; `calendar.ics` in dry run) | Object key of the feed, including a random path segment, e.g. `3f9c…/neptunus-o8-1.ics`. `state.json` is stored in the same "directory". **Required unless `DRY_RUN`** |
| `CAL_NAME` | `Neptunus-Schiebroek O8-1` | Calendar name (`NAME` / `X-WR-CALNAME`) |
| `DEFAULT_DURATION` | `1h` | Event length when the API has no end time (Go duration) |
| `RETAIN_DAYS` | `180` | How long matches no longer returned by the API stay in the feed |
| `NOTIFY_URL` | (empty) | ntfy topic URL. Empty disables notifications |
| `DRY_RUN` | `false` | Write to `OUT_DIR` instead of R2 and print a per-UID change summary |
| `OUT_DIR` | `./out` | Dry-run output directory (also where the previous `state.json` is read from) |
| `ALLOW_EMPTY` | `false` | Allow publishing zero events when upcoming matches existed |
| `DUMMY_RESCHEDULE` | `false` | Dummy only: move one synthetic match, to see a `SEQUENCE` bump |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` (JSON logs on stderr) |

See [.env.example](.env.example). Never commit a filled-in `.env`.

## Local development

```sh
go test ./...
go vet ./...

# Default loop: dummy data, written to ./out
SOURCE=dummy DRY_RUN=1 go run ./cmd/knvb-ical
# Run again: "0 change(s), nothing written"
SOURCE=dummy DRY_RUN=1 DUMMY_RESCHEDULE=1 go run ./cmd/knvb-ical
# → "updated M900000002@voetbal.nl seq=1 start end location"

# Against the real API, still writing locally
set -a; . ./.env; set +a
SOURCE=live DRY_RUN=1 go run ./cmd/knvb-ical
```

The golden feed lives in `internal/calendar/testdata/feed.golden.ics`. After an
intentional rendering change, run `go test ./internal/calendar -update`.

> **Fixtures.** Everything in `internal/fixtures/` is a real response,
> captured 2026-10-03 (`poolprogram.json`) and 2026-10-20 (the rest). Still
> unconfirmed: the format of `AssemblyTime`/`DepartureTime` (null in every
> capture; values that parse as a timestamp are shown as `HH:MM`, anything else
> verbatim) and the entries of `TeamLatestResults` (empty so far, so matches
> are looked up at any depth inside `LatestResults`).

## Cloudflare R2 setup

1. **Create the bucket.** Dashboard → *R2 Object Storage* → *Create bucket*
   (e.g. `knvb-ical`, location automatic). Note the **Account ID** shown on the
   R2 overview page.
2. **Connect a custom domain.** Bucket → *Settings* → *Public access* →
   *Custom Domains* → *Connect Domain* (e.g. `cal.example.nl`; the zone must be
   in the same Cloudflare account). Leave the `r2.dev` subdomain **disabled**.
3. **Create a scoped API token.** *R2* → *Manage API tokens* → **Create
   Account API token**, not a User API token. An account token belongs to the
   account rather than to your login, so it survives changes to your user, and
   it is the kind meant for services. Permission **Object Read & Write**,
   *Apply to specific buckets only* → your bucket. Copy the **Access Key ID**
   and **Secret Access Key** (not the token value) into `R2_ACCESS_KEY_ID` /
   `R2_SECRET_ACCESS_KEY`.
4. **Choose an unguessable key.** The URL is the only access control:

   ```sh
   echo "$(openssl rand -hex 16)/neptunus-o8-1.ics"   # → R2_OBJECT_KEY
   ```

   The feed is then at `https://cal.example.nl/<random>/neptunus-o8-1.ics`.
   `state.json` sits under the same prefix, so anyone with the feed URL can read
   it. It contains only the same match data.

Objects are uploaded with `Content-Type: text/calendar; charset=utf-8` and
`Cache-Control: max-age=300`.

### Keeping costs at zero

R2 needs a payment method, and Cloudflare has **no hard spending cap**. The
exposure is small, though. Egress is free, and this job's own usage (a few
operations a day, a ~10 KB object) is far inside the free tier. The only
realistic cost is **Class B (read) operations** if the feed URL gets hammered:
beyond the 10M free reads a month they are billed per million (see
[R2 pricing](https://developers.cloudflare.com/r2/pricing/) for current rates).
Do the following so a flood never reaches the bucket:

1. **Cache the feed at the edge.** Cloudflare doesn't cache `.ics` by default,
   so without this every request counts as a bucket read. In the zone of the
   custom domain: *Caching* → *Cache Rules* → *Create rule*. Match
   `Hostname equals cal.example.nl`, set *Eligible for cache*, and keep the edge
   TTL at "use cache-control header if present". Requests are then served from
   cache, and R2 sees at most a few reads per 5 minutes per data centre.
   Updates can appear up to 5 minutes late, which is negligible next to
   Google's refresh delay.
2. **Rate-limit the hostname.** *Security* → *WAF* → *Rate limiting rules*. The
   free plan includes one rule, e.g. `Hostname equals cal.example.nl`, more
   than 30 requests per 10 seconds per IP → *Block* for 10 seconds. Calendar
   clients poll a few times a day.
3. **Keep the `r2.dev` subdomain disabled**, so the custom domain, with its
   cache and WAF, is the only way in.
4. **Set billing alerts.** *Notifications* → *Add* → usage-based billing for
   R2 (e.g. Class B operations above 5M a month). These alerts only notify;
   they don't stop usage.
5. **Cap it at the bank.** For a real limit, pay with a virtual card that has a
   low monthly limit (many banks and fintechs offer one). An unexpected bill is
   then declined instead of charged.

## Subscribe in Google Calendar

On the desktop web UI: *Other calendars* → **+** → **From URL** → paste the
`https://…/neptunus-o8-1.ics` URL → *Add calendar*. It then also shows on
phones signed in to the same account.

> Google refreshes subscribed calendars on its own schedule, typically every
> **~8–24 hours**, and there is no way to force it. A change published by this
> job can take that long to appear. Apple Calendar and most other clients honour
> the feed's `REFRESH-INTERVAL` (6 h) and let you refresh manually.

## Container image

[`.github/workflows/image.yaml`](.github/workflows/image.yaml) runs `go vet` and
`go test -race`, then builds a multi-arch (`linux/amd64`, `linux/arm64`) image
and pushes it to `ghcr.io/tiesmaster/knvb-matches-to-ical`:

| Trigger | Tags |
|---|---|
| Push to `main` | `latest`, `sha-<short>` |
| Tag `1.2.3` or `v1.2.3` | `1.2.3`, `1.2`, `sha-<short>` |
| Pull request | Build only, nothing pushed |

To release: `git tag 0.2.0 && git push origin 0.2.0`. A plain `git push`
doesn't push tags.

The image is a static `CGO_ENABLED=0` binary on
`gcr.io/distroless/static:nonroot` (uid 65532). After the first push, make the
GHCR package public, or give the cluster an `imagePullSecret`.

## Running in Kubernetes

Deployment manifests live in the homeops repo. What the job needs:

- **A CronJob**, e.g. `schedule: "17 6,18 * * *"` with
  `timeZone: Europe/Amsterdam` and `concurrencyPolicy: Forbid`. The client
  already retries, so `backoffLimit: 0` is fine. A run takes seconds;
  `activeDeadlineSeconds: 600` is plenty.
- **Env** from the configuration table: `SOURCE=live` plus the `KNVB_*`,
  `R2_*` and (optionally) `NOTIFY_URL` values from a Secret. Treat
  `R2_OBJECT_KEY` and `NOTIFY_URL` as secrets too, because their random
  segments are the only access control.
- **Hardening that works:** `runAsNonRoot`, `readOnlyRootFilesystem: true`, no
  service-account token, all capabilities dropped. The process writes nothing
  to disk outside `DRY_RUN`. 32Mi request / 128Mi limit is ample.
- **Egress** to `app-vnl-production.sportlink.com`,
  `<account>.r2.cloudflarestorage.com` and the ntfy host.

The exit code is non-zero whenever nothing new could be published (API,
decode, state or upload errors, or the empty-feed guard). Alert on failed Jobs.

## Caveats

This uses a reverse-engineered, login-protected API, and Sportlink sells the
same feed as an in-app purchase (REQUIREMENTS §6–7). Keep it personal, don't
redistribute the data, and expect breakage when the app's client credentials
or API versions change.

## License

[MIT](LICENSE). The licence covers this code only, not the data the API
returns.
