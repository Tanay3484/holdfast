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

## The unlock game

The four core pieces are written by hand ([tasks H1–H4](specs/001-holdfast-mvp/tasks.md)). Until each one
is written, the parts of the site it powers show a "WIP" tile naming the file to open:

| Level | File | Reveals |
|-------|------|---------|
| 1 · Signature check | `internal/verify/verify.go` | "Is it really from Stripe?", held forgeries |
| 2 · Smart retries | `internal/policy/policy.go` | Attempt history, outages that recover by themselves |
| 3 · What changed | `internal/shape/shape.go` | "What changed since the last one that worked" |
| 4 · Safe resend | `internal/replay/replay.go` | Resend buttons, duplicates stopped |

## Deploy

`render.yaml` is a Render Blueprint: the web service with a 1 GB disk, and a private Ollama service with
gemma3:1b baked in. Real routes go in `HF_ROUTES` in the dashboard, never in the repo.

## How this repo is built

Every feature goes through gated phases (brief, requirements, design, tasks, implementation), each approved
by a human. See [specs/README.md](specs/README.md).

## Licence

MIT
