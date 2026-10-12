# REQUIREMENTS — iCal feed of KNVB youth-team matches via the Voetbal.nl API

Investigation only. Target team: **Neptunus-Schiebroek O8‑1**
(`PublicTeamId = T1779014083` — **verified correct** via `ClubTeams`, club `BBDX35H`).

> **Status: the integration is fully reverse-engineered and verified live (2026-10-03).**
> The static app credential does **not** work (401); the **legacy password grant works**
> and is the path to use. Helper scripts in this repo (`fetch-team-program.sh`,
> `list-club-teams.sh`, `vnl-get.sh`) perform real logins and calls. A real decoded
> response is saved as `poolprogram.json` (use it as the dummy/test fixture).
>
> **Content caveat (not a contract gap):** O8‑1 plays in the **"Zwaluwen"** mini-format,
> which is **pool-based**. As of 2026-10-03 the pool has only one match left (end of the
> *1e fase*); the next phase's fixtures (through the Christmas break) publish in a few
> weeks. The official app shows this same sparse list today. So you can build now against
> the verified contract and the data will fill in automatically. See §11.

## 0. TL;DR / recommendation

- The official **Voetbal.nl** Android app does **not** talk to voetbal.nl. It talks to
  the **Sportlink** backend (Dexels "Navajo" platform). Production base URL:
  **`https://app-vnl-production.sportlink.com`**.
- The team programme is one clean JSON call:
  `GET /entity/common/memberportal/app/team/TeamProgram?v=3&PublicTeamId=T1779014083`.
  Match venue/address/assembly time come from a second call per match
  (`MatchFacility?v=3&PublicMatchId=…`).
- **Auth (RESOLVED).** All match/programme endpoints require a **user OAuth Bearer
  token**. The static app Basic credential returns **401 "Unauthorized (proxy)"** and is
  **not** usable for match data (confirmed live). The **legacy OAuth2 password grant is
  enabled** and is the practical path: POST username+password+client_id+secret to
  `/oauth/token` → `access_token` (TTL **3600 s**) + **`refresh_token`** → call the entity
  endpoints with `Authorization: Bearer`. See §5.
- **Responses are gzip/brotli-compressed.** A raw client must send `Accept-Encoding` and
  decompress (curl: `--compressed`). Without it the body is binary garbage.
- **No certificate pinning, no Play Integrity / attestation** gates these calls. A
  headless client is technically fine; the only real obstacle is obtaining & refreshing a
  user token unattended.
- There is an **official per-team ICS feed** already (`TeamCalendarLink` →
  `CalendarLink`), but it is a **paid in-app purchase** (`UserCalendars` carries
  `Price`/`ProductSku`, Apple/Google IAP). See §6 — relevant to ToS and to "is there a
  sanctioned path".
- **Fallback** (VoetbalAssist club site) remains the lowest-friction, no-auth option and
  is still recommended as the default unless you specifically want app-grade fields
  (assembly time, dressing rooms). See §8.

---

## 1. Artifact analysed

| Item | Value |
|---|---|
| App | Voetbal.nl – De officiële app (publisher Voetbalmedia B.V.) |
| Package | `com.xs2theworld.voetballNL` |
| Version analysed | **6.64.0** (versionCode `2026025447`), Play "updated 18 Sep 2026" |
| Flavor | `voetbalnl` (`BuildConfig.FLAVOR`), `DEFAULT_DTAP = PRODUCTION` |
| min/target SDK | 24 / 36 |
| Framework | Native Kotlin (Retrofit + OkHttp + RxJava + Gson). No Flutter/RN. |
| App code package | `com.dexels.sportlinked` (Dexels = Sportlink vendor) |

### 1.1 Provenance & signature (how it was obtained)

No Android device was attached over USB in this environment (`adb` unavailable), so the
APK could not be pulled from your own device. It was downloaded from **APKPure** via
`apkeep` and then **verified**:

- Downloaded as an XAPK (split APKs: `base`, `config.armeabi_v7a`, `config.xhdpi`).
- `apksigner verify` on every split: **verifies**, v2+v3 schemes.
- Signing certificate: `CN=JW, OU=Mobile, O=XS2TheWorld, L=Amsterdam, C=NL`,
  SHA‑256 `2ae3386b7479e0b6dcafb4ad5b6054bba2d68bea9be37309e4ca88e8087870f0`.
- **Play source stamp present and valid** (`Source Stamp Signer … O=Google Inc.`),
  i.e. this build came from Google Play — strong evidence it was not tampered with by the
  mirror.
- XAPK SHA‑256: `35f496a1201bcfebc3e49d1da4d8214e7d6e3af67382b727cca78a16b39d54b9`.

> ⚠️ Before trusting this long-term, ideally re-pull the APK from your own phone
> (`adb shell pm path com.xs2theworld.voetballNL` → `adb pull …`) and compare the signer
> certificate SHA‑256 above. Same signer = same app.

Tooling used: `apkeep` (fetch), `apksigner` (verify), `jadx` 1.5.6 (decompile
`classes4.dex`, which holds `com.dexels.sportlinked`). Static analysis only — no requests
were made to Sportlink servers, and the app was never run.

---

## 2. Backend, hosts, versioning

### 2.1 Host resolution (from `Dtap.makeBackend()` / `DtapKt`)

The flavor segment is `"vnl"` (`DtapKt.a()` returns `vnl` for the voetbalnl app).

| DTAP | Entity base URL | Binary store |
|---|---|---|
| **PRODUCTION** (default) | `https://app-vnl-production.sportlink.com` | `https://binaries.sportlink.com` |
| PRODUCTION_STAGING | `https://app-vnl-staging.sportlink.com` | `https://binaries.sportlink.com` |
| ACCEPTANCE | `https://voetbalnl.acceptance.sportlink.com` | `https://binaries.acceptance.sportlink.com` |
| TEST | `https://voetbalnl.devnl.sportlink.com` | `https://binaries-playground.sportlink.com` |
| LOCAL_DEVELOPMENT | `http://192.168.1.64:8181` | `https://binaries-playground.sportlink.com` |

Auth server (separate host, see §5): **`https://app-auth.sportlink.com`**.
Other hosts seen: `cfapi.voetbal.nl` (legacy), `app-logger.sportlink-infra.net` /
`ccproxy.sportlink-infra.net` (telemetry/config), `cdn-eu.configcat.com` (feature flags).

### 2.2 API shape & versioning

- REST-ish "entity" calls over the Navajo platform. Path template:
  `entity/common/memberportal/app/<area>/<Entity>?v=<N>`.
- Two layers of versioning: a `?v=<N>` query **and** an `X-Navajo-Version: <N>` header
  (Retrofit `@Headers`). Keep both in sync with what the app sends.
- Responses are JSON (Gson). Field names are PascalCase via `@SerializedName` (documented
  in §4). No GraphQL.
- Entity GETs are cached by OkHttp (100 MB disk cache in app; a server may send
  `Cache-Control`). A `PutToGetInterceptor` lets the app tunnel some GETs as POST — not
  needed for plain reads.

---

## 3. Endpoints relevant to an iCal feed

All paths below are relative to the production base URL and require the auth headers in
§5.3. "Auth path" = which OkHttp client the app uses (`entity` = user Bearer token;
`publicEntity` = static app Basic only).

| Purpose | Method & path | Key query params | Auth path |
|---|---|---|---|
| **Team programme (upcoming)** | `GET entity/common/memberportal/app/team/TeamProgram?v=3` | `PublicTeamId` | entity |
| Team recent results (past) | `GET entity/common/memberportal/app/team/TeamLatestResults` | `PublicTeamId`, `MaxResults?`, `PublicMatchId?` | entity |
| Match facility (venue/address/field/assembly) | `GET entity/common/memberportal/app/match/MatchFacility?v=3` | `PublicMatchId` | entity |
| Match info (category/attributes) | `GET entity/common/memberportal/app/match/MatchInfo?v=1` | `PublicMatchId` | entity |
| Match result detail (score/lineup) | `GET entity/common/memberportal/app/match/MatchResultDetails?v=8` | `PublicMatchId` | entity |
| Single programme item | `GET entity/common/memberportal/app/program/ProgramItemMatch?v=2` | `DetailsKey` | entity |
| Club programme (all teams) | `GET entity/common/memberportal/app/club/ClubProgram?v=3` | `ClubId` | entity |
| Club teams (discover team IDs) | `GET entity/common/memberportal/app/club/ClubTeams?v=1` | `ClubId` | entity |
| Resolve/search a team | `GET entity/common/memberportal/app/user/favorite/FavoriteSearchTeam?v=2` | `PublicTeamId`  *or*  `Query`,`TeamName?` | entity |
| **Official per-team ICS link** (paid, see §6) | `GET entity/common/memberportal/app/team/TeamCalendarLink` | `PublicTeamId` | entity |

### 3.1 Sample request (team programme)

```http
GET /entity/common/memberportal/app/team/TeamProgram?v=3&PublicTeamId=T1779014083 HTTP/2
Host: app-vnl-production.sportlink.com
Authorization: Bearer <REDACTED_USER_ACCESS_TOKEN>
X-Navajo-Instance: KNVB
X-Navajo-Version: 3
X-Navajo-Locale: nl
User-Agent: sportlink-app-voetbalnl/6.64.0-2026025447 android <model>/<mfr>/<sdk> <md5(token)> (6.64.0)
Accept: application/json
```

### 3.2 Sample response shape (TeamProgram, abbreviated, synthetic values)

```jsonc
{
  "ProgramItemMatch": [
    {
      "DetailsKey": "…",                 // ProgramItem base: opaque key
      "Timestamp": "2026-10-11T09:00:00",// ProgramItem base (start)
      "EndTimestamp": "2026-10-11T10:00:00",
      "Match": {
        "PublicMatchId": "M809680625",   // STABLE id → use for iCal UID
        "ExternalMatchId": 36831,        // "wedstrijdnummer" (human number)
        "MatchDateTime": "2026-10-11T09:00:00",
        "Status": "",                    // "", "Afgelast", "Gestaakt", … (free string)
        "DisciplineStatus": null,
        "Location": "Thuis",             // free string (thuis/uit or venue label)
        "HomeTeam": {
          "PublicTeamId": "T1779014083",
          "TeamName": "Neptunus-Schiebroek O8-1",
          "TeamCode": "…",
          "Club": { "ClubId": "BBDX35H", "ClubName": "Neptunus-Schiebroek", "City": "Rotterdam" }
        },
        "AwayTeam": { "…": "…" },
        "Pool": { "…": "…" }
      }
    }
  ],
  "ProgramItemMatchSportlinkClub": [ /* same Match + "MatchType" */ ],
  "ProgramItemTournamentMatch": [ /* Match + HighlightHome/Away */ ],
  "ProgramItemAnnouncement": [],
  "ProgramItemClubTournament": []
}
```

> `TeamProgram` gives teams, kickoff, status and a rough `Location`, but **not** the full
> address, field number, dressing room or assembly time. For those, call
> `MatchFacility?v=3&PublicMatchId=<id>` per match (fields in §4.3).

---

## 4. Field mapping → iCal VEVENT

### 4.1 Available fields by source

**`Match` (in TeamProgram):** `PublicMatchId`, `ExternalMatchId`, `MatchDateTime`,
`Status`, `DisciplineStatus`, `Location`, `HomeTeam`, `AwayTeam`, `Pool`.
**`Team`:** `PublicTeamId`, `TeamName`, `TeamCode`, `Gender`, `LogoUrl`, `Club`.
**`Club`:** `ClubId`, `ClubName`, `City`, `ClubLogo`, `ClubColors`.
**`ProgramItem` base:** `DetailsKey`, `Timestamp`, `EndTimestamp`.
**`MatchFacility` (extends `Facility`):** `Address`, `City`, `ZipCode`, `FacilityName`,
`PhoneNumber`, `FacilityId` (from Facility) + `AssemblyTime`, `DepartureTime`,
`HomeDressingRoom`, `AwayDressingRoom`, `OfficialDressingRoom`, `FieldType`,
`SubFacilityName`, `ClubInformationUrl`.

### 4.2 Suggested VEVENT mapping

| VEVENT property | Source |
|---|---|
| `UID` | `Match.PublicMatchId` + `@voetbal.nl` (stable; see §4.4) |
| `DTSTART` | `Match.MatchDateTime` (or `ProgramItem.Timestamp`), `Europe/Amsterdam` |
| `DTEND` | `ProgramItem.EndTimestamp` if present, else `DTSTART + ~1h` |
| `SUMMARY` | `"{HomeTeam.TeamName} – {AwayTeam.TeamName}"` |
| `LOCATION` | `MatchFacility`: `"{FacilityName}, {Address}, {ZipCode} {City}"` (+`SubFacilityName`/field) |
| `GEO` | none — API has **no lat/long**; address only (geocode externally if needed) |
| `DESCRIPTION` | assembly time (`AssemblyTime`), `DepartureTime`, dressing rooms, `FieldType`, `Pool`, `ExternalMatchId` (wedstrijdnr), `Status`/`DisciplineStatus` |
| `STATUS` | `CONFIRMED`; map `Match.Status`=="Afgelast"→`CANCELLED` (see §4.3) |
| `URL` | deep link to match (`sportlinked-voetbalnl://…`) or voetbal.nl match page |
| `LAST-MODIFIED`/`SEQUENCE` | bump when any mapped field changes (drives calendar updates) |

### 4.3 Status handling

`Match.Status` and `DisciplineStatus` are **free-form server strings**, not an enum in the
app. Expected Dutch values to handle: `""`/`"Gespeeld"` (normal), `"Afgelast"`
(cancelled), `"Gestaakt"` (abandoned), `"Uitgesteld"`/`"Verplaatst"` (postponed/moved).
Exact spellings must be confirmed live (§9). Map cancelled/abandoned → VEVENT
`STATUS:CANCELLED`; for moved matches the new `MatchDateTime` should arrive on the next
poll (bump `SEQUENCE`).

### 4.4 Stable identifier for UID

`PublicMatchId` (e.g. `M809680625`) is the stable, opaque match id used across all match
endpoints — **use it for the iCal `UID`**. `ExternalMatchId` (the "wedstrijdnummer", e.g.
`36831`) is human-facing and good for the description, but prefer `PublicMatchId` for UID
stability. `PublicTeamId` (`T…`) identifies the team feed.

### 4.5 Past vs future

`TeamProgram` is the **upcoming** programme (like the website). **Past** matches come from
a different endpoint, `TeamLatestResults` (`PublicTeamId`, optional `MaxResults`). If the
iCal feed should include played matches, merge both.

> For **O8‑1 (Zwaluwen)** the programme is **pool-based**, not team-based — see §11. Right
> now it holds a single end-of-phase match; the next phase publishes in a few weeks.

---

## 5. Authentication & lifecycle

The app contains **two** token mechanisms. The interceptor
(`OAuthTokenInterceptor`) picks per-token via `token.isBffToken`.

### 5.1 BFF AppAuth (current / primary) — OIDC via a server-side broker

- Config: `appId = "voetbalnl-mobile-bff"`, base `https://app-auth.sportlink.com`,
  redirect `sportlinked-voetbalnl://oauth2/callback/`.
- Flow:
  1. `POST /oauth/v1/start` `{appId, redirectUri}` → `{authUrl, state}`.
  2. App opens `authUrl` in a browser/Custom Tab; user signs in with their **KNVB
     account** on Sportlink's auth page; redirect back to the custom scheme with
     `code` + `state`.
  3. `POST /oauth/v1/exchange` `{appId, code, state, redirectUri}` →
     `{accessToken, expiresIn, tokenType, refreshToken}`.
  4. Refresh: `POST /oauth/v1/refresh` with header `Authorization: <current token>` and
     body `{appId}`. Note the **RefreshRequest only carries `appId`** — the refresh token
     is held/rotated **server-side by the BFF** (cookie/session), not sent as a form
     field. This is a classic "backend-for-frontend" pattern.
  5. Logout: `POST /oauth/v1/logout`.
- This is PKCE/`state`-protected but the heavy lifting (client secret, token storage) is
  on the BFF. A headless client would have to replicate the browser login + custom-scheme
  redirect capture once, then rely on `/oauth/v1/refresh`.

### 5.2 Legacy OAuth2 (password / refresh grant) — ✅ VERIFIED, USE THIS

- `POST /oauth/token` (form-encoded):
  - login: `grant_type=password`, `username`, `password`, `client_id`, `secret`
  - refresh: `grant_type=refresh_token`, `refresh_token`, `client_id`, `secret`
- Production client for KNVB/voetbalnl: `client_id = "<CLIENT_ID — see KNVB_CLIENT_ID>"`,
  `secret = <REDACTED — hard-coded in app>` (acceptance/test builds use a different
  pair). These are **static app credentials**, not per-user.
- **Verified live 2026-10-03:** password grant is enabled. A successful login returns
  `access_token`, `expires_in = 3600` (1 h), and a **`refresh_token`**. Wrong credentials
  return HTTP 400 `{"error":"Inloggen niet mogelijk","description":"onjuiste
  inloggegevens"}`; the app client_id/secret are accepted. This is the path the helper
  scripts use and the recommended path for the headless service.
- Error/response notes: the login error body is Dutch; the field names on success are
  snake_case (`access_token`, `expires_in`, `refresh_token`) — note this differs from the
  BFF exchange response which is camelCase.

### 5.3 Headers every data call needs (`SportlinkHeadersInterceptor`)

- `X-Navajo-Instance: KNVB`  (from `getDefaultUnionId()` for this flavor)
- `X-Navajo-Locale: nl`
- `User-Agent: sportlink-app-voetbalnl/<version>-<code> android <Build.MODEL>/<MANUFACTURER>/<SDK_INT> <md5(accessToken)> (<version>)`
  — note the UA embeds an **MD5 of the access token**; a token-less call sends an empty
  field there. Not known to be enforced, but mimic it to look like the app.
- Plus `Authorization: Bearer <token>` (entity path) **or** `Authorization: Basic <app>`
  (publicEntity path).

### 5.4 Token lifetime & storage

- `expiresIn` is seconds; expiry = `now + expiresIn*1000`. **Verified: `expires_in = 3600`
  (1 h).** Refresh before expiry with `grant_type=refresh_token`.
- `isExpired()` is a simple wall-clock check; the interceptor refreshes **synchronously**
  with a 3 s timeout when expired.
- A `refresh_token` **is** returned. Its TTL is not encoded in the app → still **unknown
  how long unattended refresh survives** before a full re-login is needed. Design the
  service to fall back to a username/password re-login when refresh fails (§7, §11).

### 5.5 Does anything work without a user login? — ✅ RESOLVED: no

- The static **app Basic credential** (`Sportlink.App` / `<REDACTED>`) is used in the app
  **only** for registration/password endpoints.
- **Verified live:** calling `TeamProgram` with that Basic credential (or none, or bogus)
  returns **HTTP 401 `{"Error":true,"Status":"401","Message":"Unauthorized (proxy)"}`**.
  A gateway in front of Navajo rejects anything without a user Bearer token. So a user
  account **is** required; there is no app-key-only read path.

---

## 6. The official ICS feed (paid IAP)

The app already exposes per-team calendar links:

- `GET team/TeamCalendarLink?PublicTeamId=…` → `{ "CalendarLink": "<url>" }`
- `GET user/UserCalendarLink` → `{ "CalendarLink": "<url>" }`
- `GET user/calendar/UserCalendars?v=2` → `{ HasUserCalendar, Price, ProductSku,
  TeamCalendar:[{…, HasCalendar}] }`
- `POST user/calendar/UserCalendarPurchase?v=1`, `GET …/UserCalendarPurchaseStatus?v=1`

`Price` + `ProductSku` + the Apple/Google purchase objects show the calendar
subscription is a **paid in-app purchase**. Implication: Sportlink sells exactly the ICS
feed you want. Two takeaways:
1. There may be a **sanctioned** route — buy the calendar once in the app, read the
   `CalendarLink` it returns, and just subscribe your homelab/Google Calendar to that URL.
   That could be the cleanest, lowest-risk solution and sidesteps all token plumbing.
   (Confirm the returned URL is a plain unauthenticated ICS/webcal link.)
2. Because they monetise this, scraping/synthesising the same feed for free is more
   ToS-sensitive than usual (§7).

---

## 7. Feasibility for a headless homelab (Kubernetes) service

**Technical gating: low.**
- No certificate pinning (no `network_security_config.xml`, no `CertificatePinner`
  configured on the app's OkHttp client).
- No Play Integrity / SafetyNet / attestation anywhere in the app code path.
- Plain HTTPS + JSON. A `curl`/Python client with the right headers works if it has a
  valid token.

**Auth gating: the real problem.**
- Easiest *if it works*: legacy password grant (§5.2) — store KNVB username/password as a
  k8s Secret, do `grant_type=password` → cache `access_token`/`refresh_token`, refresh
  before expiry. Risk: password grant may be disabled server-side.
- Otherwise: BFF flow (§5.1) needs an interactive browser login once to capture a
  `code`; thereafter `/oauth/v1/refresh` *may* keep it alive, but the refresh token lives
  server-side and its lifetime is unknown — may force periodic manual re-login.
- Cleanest/safest: **buy the calendar IAP and consume the returned `CalendarLink`** (§6),
  or use the club-site fallback (§8) — neither needs a long-lived token.

**Polling / rate limits.**
- No rate-limit constants in the app. Be polite: youth fixtures change rarely — poll
  **once or twice a day** (e.g. a k8s CronJob), with conditional requests / respect any
  `Cache-Control`. Avoid tight loops; back off on `600` (the app's "token invalid"
  pseudo-status) and 429/5xx.

**Fragility to app updates.**
- Endpoint paths + `?v=`/`X-Navajo-Version` are stable-ish but **do bump** across
  versions (e.g. `MatchResultDetails` is already at v8, `UserProgram` v10). A pinned
  client can break when the server retires an old `v`. Mitigate by mirroring a current
  app version's versions and watching for `TeamProgram` schema drift.
- Hard-coded client credentials can be rotated by the vendor at any time, breaking the
  legacy path without notice.

**ToS / legal.**
- This is KNVB/Sportlink member data behind a login, and the same feed is **sold** as an
  IAP. Automating account login to extract it is very likely against the app/website
  terms, even for personal use. The **club-site fallback** (public VoetbalAssist page) and
  the **paid official ICS** are the defensible options. Recommend treating the reverse-
  engineered API as research / last resort, and not redistributing data or credentials.
- Never commit or log tokens/credentials. Redact in all notes (done here). The app even
  hashes the token into its User-Agent — treat tokens as secrets end to end.

---

## 8. Comparison with the club-site fallback (VoetbalAssist)

| Dimension | Sportlink app API | VoetbalAssist club site (`neptunus-schiebroek.nl/500/uitslagen--programma/`) |
|---|---|---|
| Auth | User OAuth token (or paid IAP) | **None** (public page) |
| Format | Clean JSON | Server-rendered HTML (scrape) |
| Fields | Rich: assembly time, dressing rooms, field type, address, pool, status | Typically date/time, home/away, venue label, sometimes field; no assembly time/dressing room |
| Stable UID | `PublicMatchId` | Must derive from match number/row; less clean |
| Past matches | `TeamLatestResults` | Usually both programme + results on page |
| Fragility | app-version/`v=` drift, token lifetime, cred rotation | HTML layout changes |
| ToS risk | **High** (login data, sold as IAP) | **Low** (public club page) |
| Effort | High (auth plumbing) | Low–medium (HTML parse) |

**Decision:** build against the **Sportlink API** (this document). The club-site fallback
remains a useful **cross-check / degraded-mode source** if the API path breaks (cred
rotation, grant disabled, etc.) — worth keeping behind the same interface as a second
`live` provider.

---

## 9. Open questions — status

| # | Question | Status |
|---|---|---|
| 1 | Does the static app Basic cred read `TeamProgram`? | ❌ **Resolved: no** — 401 "Unauthorized (proxy)". User token required. |
| 2 | Is password grant enabled? | ✅ **Resolved: yes** — `expires_in=3600`, refresh token returned. |
| 3 | Refresh-token lifetime / unattended survival? | ⏳ **Still open** — refresh token exists; its TTL unknown. Build a re-login fallback. |
| 4 | Buy the official IAP `CalendarLink` instead? | ✅ **Decided: no** — we build it ourselves. |
| 5 | Scope: upcoming/past, one team / whole club? | ⏳ Your call (default: O8‑1 upcoming). See §11. |
| 6 | Enrich with `MatchFacility` per match? | ⏳ Your call — `Location` is often `null` in the pool feed, so facility enrichment is likely needed for a useful `LOCATION`. |
| 7 | ToS comfort automating a logged-in account? | ⏳ Your call — you've chosen to build; keep it personal, don't redistribute. |
| 8 | Exact `Status` string values? | 🟡 Partially — observed `"SCHEDULED"`. Cancelled/moved spellings still to capture when fixtures publish. |

---

## 10. Verified request/response facts (2026-10-03, app v6.64.0)

- **Login:** `POST /oauth/token` form `grant_type=password&username&password&client_id=<CLIENT_ID — see KNVB_CLIENT_ID>&secret=<REDACTED>`
  → `200 {access_token, token_type, expires_in:3600, refresh_token}`.
- **Auth required:** `TeamProgram` with Basic/none/bogus → `401 {"Error":true,"Status":"401","Message":"Unauthorized (proxy)"}`.
- **Compression:** responses are gzip/brotli — must decompress (`curl --compressed`).
- **Team id verified:** `ClubTeams?v=1&ClubId=BBDX35H` lists all Neptunus teams;
  O8‑1 = `T1779014083` (and O9‑2 = `T750484052`), confirming the prior IDs.
- **Real `Match` fields observed** (see `poolprogram.json`): `MatchDateTime`
  `"2026-10-03T08:30:00+0200"` (ISO‑8601 **with** `+0200` offset — no separate TZ lookup
  needed), `Status:"SCHEDULED"`, `ExternalMatchId:36736`, `Location:null` (in the pool
  feed), nested `HomeTeam`/`AwayTeam` with full `Club` incl. `ClubLogo{Bucket,Hash}`
  (fetch logos from the binary store: `GET {binaryStoreUrl}/{Bucket}/{Hash}`), and `Pool`
  with `PoolId`, `ClassName`, `NextMatchDate`, `PreviousMatchDate`.

---

## 11. Implementation contract for the tool (O8‑1 / Zwaluwen)

### 11.1 The Zwaluwen nuance (why O8‑1 looks empty)

O8‑1 plays in the **"Zwaluwen"** mini-format: it belongs to a **pool**
(`PoolId 839615`, "4e klasse Zwaluwen Jeugd (1e fase)") rather than having individual
`Neptunus O8‑1 vs X` fixtures. Observed behaviour:

- `TeamCompetitionData?v=2&PublicTeamId=T1779014083` → the team's pool(s) with `PoolId`.
- `TeamProgram?v=3&PublicTeamId=…` and `PoolProgram?v=2&PoolId=…` currently return **one**
  match (`VOC O8‑5 vs BVCB O8‑4`, 2026‑10‑03) — the **last of the 1e fase**. `NextMatchDate`
  is `null`. This is not a bug and not a wrong id: the official app shows the same. The
  next phase's fixtures (through the Christmas break) will appear in a few weeks.
- Matches in this feed may not name a Neptunus team at all, and `Location` may be `null`.

**Design implication:** the data source for O8‑1 is the **pool programme**, and the
contract is proven but the content is seasonal. Build now; it fills in automatically.

### 11.2 Recommended fetch flow (per refresh)

1. **Auth:** password grant → cache `access_token` (3600 s) + `refresh_token`. On expiry,
   refresh; if refresh fails, re-login with stored username/password.
2. **Resolve pool(s):** `GET team/TeamCompetitionData?v=2&PublicTeamId=<team>` → `Pool[].PoolId`.
   (Cache; changes only between phases.)
3. **Fetch fixtures:** for each `PoolId`, `GET pool/PoolProgram?v=2&PoolId=<id>`
   (and/or `GET team/TeamProgram?v=3&PublicTeamId=<team>` — same shape, team-scoped).
4. **(Optional) enrich:** per `PublicMatchId`, `GET match/MatchFacility?v=3&PublicMatchId=<id>`
   for `Address`/`City`/`ZipCode`/`AssemblyTime`/`DepartureTime`/dressing rooms/`FieldType`
   (N+1 calls; needed because the pool feed's `Location` is often null).
5. **(Optional) past:** `GET team/TeamLatestResults?PublicTeamId=<team>` for played matches.
6. **Render `.ics`** per the §4 mapping; `UID = PublicMatchId@voetbal.nl`.

> For **older teams** (not Zwaluwen) `TeamProgram` returns the team's own fixtures directly,
> so the same flow works — steps 2–3 just collapse to a single `TeamProgram` call. Make the
> pool indirection a code path that activates when `TeamProgram` is empty/pool-shaped.

### 11.3 Feature-flag / dummy strategy (recommended given seasonal content)

Because live fixtures won't exist for a few weeks, structure the fetcher behind an
interface with two implementations:

- **`dummy`** (default-on now): returns parsed fixtures from a bundled fixture file —
  **`poolprogram.json`** in this repo is a real decoded response and is the ideal seed.
  Add a couple of synthetic future-dated matches to exercise the `.ics` rendering.
- **`live`** (behind a flag, e.g. `KNVB_LIVE=1`): the §11.2 flow.

This lets you finish and test the whole pipeline (parse → map → `.ics` → calendar
subscription) now, then flip the flag when the 2e fase publishes. Validate `live` against
`dummy` by diffing the mapped output for the one real match that exists today.

### 11.4 Secrets & ops

- Store KNVB username/password + `client_id`/`secret` as k8s Secrets / env vars, **not** in
  committed scripts. The helper scripts here inline the app creds for convenience — move
  them to required env vars before committing anywhere shared.
- Poll politely: **1–2×/day** (CronJob). Fixtures change rarely. Back off on 401/`600`
  (token invalid), 429, 5xx.
- Never log tokens. Rotate if a token ever lands in a log.

### 11.5 Repo helper scripts (verification / prototyping)

| Script | Purpose |
|---|---|
| `test-public-access.sh` | Proves the app Basic cred is rejected (401) — documents why auth is needed. |
| `fetch-team-program.sh` | Login + `TeamProgram` for a `PublicTeamId`; prints match count. |
| `list-club-teams.sh` | Login + `ClubTeams` for a `ClubId`; lists `PublicTeamId`/`TeamCode`/`TeamName`. |
| `vnl-get.sh` | Login + generic authenticated GET of any entity sub-path (prototyping). |

All send `--compressed` and the §5.3 headers; pass creds via `KNVB_USER`/`KNVB_PASS`.
Decompiled sources/tools are under `./.work/` (git-ignored; safe to delete — on disk, not
the RAM-backed `/tmp` that caused the earlier OOM crashes).
