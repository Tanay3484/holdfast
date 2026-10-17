# 002 Demo and Live modes — Requirements, design and tasks

Status: Draft
Owner: Tanay
Updated: 2026-10-18

Built at Tanay's request on the last night before the deadline ("overhaul the website since it's no
longer a game … two modes, Demo and Live"). Requirements, design and tasks are kept in this one file to
fit the time; review them together.

## Why

The unlock game did its job: all four levels are written. Judges and real visitors need two things
instead: a demo that works with no sign-up, and a way to put Holdfast in front of real webhooks. Tanay has
no Stripe account, so Stripe sign-in can't be built or tested; the site says so plainly.

## Requirements

### R12 Modes and landing
- **R12.1** The header SHALL offer two modes, Demo and Live, and show which one is active.
- **R12.2** The landing page SHALL explain the product, link to both modes, show a real example answer,
  and state plainly what is real today and what isn't.
- **R12.3** The public pages SHALL no longer show the unlock game (WIP tiles, progress link).

### R13 Demo
- **R13.1** `/demo` SHALL show the Chai & Co. dashboard and fault buttons, labelled as made-up data.
- **R13.2** WHEN the demo has no events, the system SHALL seed a short history once the server is up.

### R14 Live sign-in
- **R14.1** The system SHALL sign people in with a GitHub OAuth app asking only for `admin:repo_hook` and
  `read:user`, with a state check against cross-site sign-in.
- **R14.2** The system SHALL keep the session in a signed, HttpOnly, SameSite=Lax cookie, and store the
  GitHub token encrypted (AES-GCM) with a key from `HF_SESSION_KEY`.
- **R14.3** IF GitHub sign-in is not configured, THEN Live mode SHALL say so and point to the demo.

### R15 Connections
- **R15.1** A signed-in person SHALL be able to connect a GitHub repository they administer; the system
  SHALL add one webhook to it with a random secret, pointing at an unguessable `/in/c-…` address.
- **R15.2** A person SHALL be able to create a Stripe connection by hand: Holdfast shows the address to
  paste into Stripe, and accepts the `whsec_` signing secret afterwards.
- **R15.3** A person SHALL be able to create a connection for any other service, without signatures.
- **R15.4** Each connection SHALL deliver either to the person's own URL or to Holdfast's test app.
- **R15.5** IF a destination resolves to a non-public address, THEN the system SHALL refuse it when saved
  and refuse to connect to it at delivery time.
- **R15.6** A connection's pages, events and actions SHALL be visible only to its owner (404 otherwise).
- **R15.7** Disconnecting SHALL remove the GitHub webhook (or say it couldn't), the route and its events.
- **R15.8** The system SHALL limit each person to `HF_MAX_CONNECTIONS` connections (default 5).
- **R15.9** Connections SHALL survive a restart.

### R16 Test app
- **R16.1** The test app SHALL answer in one of four modes the owner picks: working, down (503), turning
  messages away (400) or too slow.
- **R16.2** Stripe and other connections SHALL offer a "send a sample event" button, labelled made up.

## Design

- **Routes at runtime.** `relay.Relay` keeps routes in a locked map (`Route`, `SetRoute`, `RemoveRoute`).
  A connection becomes a route with `Owner` set and `Guarded` set unless it uses the test app.
- **Network guard.** `internal/netguard`: `CheckURL` on save; `Transport()` refuses non-public IPs at dial
  time, so DNS that later points somewhere private is still refused. Guarded routes use it.
- **Sessions.** `internal/session`: HMAC-signed cookie (14 days), OAuth state cookie, AES-GCM `Seal`/`Open`.
- **GitHub.** `internal/github`: `AuthURL`, `Exchange`, `User`, `Repos` (admin only), `CreateHook`,
  `DeleteHook`, behind an `API` interface. `web.DevGitHub` fakes it locally with `HF_DEV_LOGIN=true`.
- **Storage.** `users` and `connections` tables in the same SQLite file. Secrets and tokens sealed.
- **Test app.** `internal/testapp`, in memory per connection, at `POST /test-app/{conn}`.
- **Pages.** `/` landing, `/demo`, `/live` (signed out: how it works and the honest Stripe note; signed in:
  connection cards), `/live/new` (GitHub, Stripe, something else), `/r/{conn}` (board plus side panels).
- **Hosting.** `render.yaml` adds `HF_SESSION_KEY` (generated) and `HF_GITHUB_CLIENT_ID/SECRET` (set by
  hand). The public URL defaults to Render's `RENDER_EXTERNAL_URL`.

## Tasks

- [x] **T22** Routes at runtime, network guard · @ai · R15.5, R15.9
- [x] **T23** Sessions, GitHub client, users and connections storage · @ai · R14, R15
- [x] **T24** Test app and sample events · @ai · R16
- [x] **T25** Landing, Demo and Live pages, connection page, mode switch · @ai · R12, R13, R15
- [x] **T26** Tests: guard, sessions, GitHub client against a fake, ownership, private destinations,
  Stripe secret, storage · @ai
- [ ] **T27** Create the GitHub OAuth app and set its id and secret on Render · @human
- [ ] **T28** Deploy, connect a real repository, push, and watch it arrive; break the test app and resend ·
  @human+ai

## Not built, said plainly

- Stripe sign-in (Stripe Connect): needs a Stripe platform account.
- Private repositories: listing them would need the broader `repo` scope; only repositories the token can
  see are offered.
