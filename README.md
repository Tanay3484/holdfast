# Holdfast

A safety net for webhooks. Holdfast sits between a provider (Stripe, GitHub) and your app. It stores every
event before acknowledging it, holds the ones your app fails on, explains why in plain words with an
open-weight model, and resends them safely once the problem is fixed.

Built for the [Hacktoberfest 2026 Open-Source AI Challenge, Week 2: Side Hustle](https://dev.to/challenges/hacktoberfest-week2-2026-10-12).

## How it works

```
provider ──▶ /in/{route} ──▶ check signature ──▶ save to disk ──▶ "got it"
                                                      │
                     deliver to your app ◀────────────┘
                       ├─ accepted      → done
                       ├─ worth a retry → wait a little longer each time
                       └─ won't work    → hold it safely
                                             │
                     code collects the facts ┘  (signature, every try, what changed since the last success)
                     Gemma names the cause from a fixed list and explains it; code checks every word
                     resend: never twice, never anything that failed its signature check
```

- **Code decides, the model explains.** Retries, holds, duplicates and resends are plain tested Go. Gemma
  only picks a cause from a fixed list and writes the explanation, and code rejects jargon, invented
  numbers and promises.
- **Open weights, on your server.** Payloads carry names and amounts; they never go to an AI company.

## Run it

Needs Go 1.27+ and [Ollama](https://ollama.com/download) with a Gemma model (`ollama pull gemma3:4b`).

```
go run ./cmd/holdfast          # http://localhost:8080 — the live demo is on the home page
go run ./cmd/unlock            # progress board for the @human levels (-v for every check)
go test ./...
```

Settings are environment variables; see [.env.example](.env.example). Without Ollama everything still works
and diagnoses come from the built-in rules.

## Two modes

- **Demo** (`/demo`): Chai & Co., a pretend tea shop with made-up customers. Break it seven ways and watch
  the real relay, retries, checks and Gemma's explanations.
- **Live** (`/live`): sign in with GitHub, pick a repository, and Holdfast adds itself as the webhook.
  Deliver to your own app or to the built-in test app, which you can switch to failing on purpose. Stripe
  connects by hand (one-click Stripe sign-in needs a Stripe platform account this project doesn't have).

To try Live mode locally without a GitHub OAuth app: `HF_DEV_LOGIN=true go run ./cmd/holdfast`. GitHub is
then simulated and no real webhooks are added.

## How it was built: the unlock game

The four core pieces (signature checks, retry policy, shape diff, resend plan) started as stubs, each a
"level" with known-answer checks in `internal/unlock`. `go run ./cmd/unlock` shows the board; while a level
was locked, the parts of the site it powers showed a "WIP" tile. All four are now unlocked.

## Deploy

`render.yaml` is a Render Blueprint: the web service with a 1 GB disk, and a private Ollama service
(`2c-4g`) with gemma3:4b baked in. For Live mode, create a GitHub OAuth app with the callback URL
`https://<your service>.onrender.com/auth/github/callback` and set `HF_GITHUB_CLIENT_ID` and
`HF_GITHUB_CLIENT_SECRET` in the dashboard. `HF_SESSION_KEY` is generated.

## How this repo is built

Every feature goes through gated phases (brief, requirements, design, tasks, implementation), each approved
by a human. See [specs/README.md](specs/README.md).

## Licence

MIT
