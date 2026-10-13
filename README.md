# Holdfast

A safety net for webhooks. It stores every event before acknowledging it, holds the ones your app fails
on, explains why in plain words, and resends them safely.

Built for the [Hacktoberfest 2026 Open-Source AI Challenge, Week 2: Side Hustle](https://dev.to/challenges/hacktoberfest-week2-2026-10-12).

**Status: planning.** The product brief is in [specs/000-brief/brief.md](specs/000-brief/brief.md). There
is no application code yet.

## How it will work

1. An incoming webhook is written to disk, then acknowledged.
2. A worker forwards it to your app, retrying on failure and holding it after repeated failures.
3. Code checks the signature, the body and how the payload's shape differs from the last good one.
4. [Gemma](https://ai.google.dev/gemma), an open-weight model, names the likely cause and writes an
   incident note a client can read.
5. Held events are resent with a duplicate guard.

No closed AI API is called. Payloads stay on the server you run.

## Development

Needs Go 1.27+.

```
go test ./...
go vet ./...
gofmt -l .
```

## How this repo is built

Every feature goes through gated phases (brief, requirements, design, tasks, implementation), each approved
by a human before the next starts. See [specs/README.md](specs/README.md).

## Licence

MIT
