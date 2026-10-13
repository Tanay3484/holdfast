# Week 2 — Side Hustle — agent guide

Hacktoberfest 2026 Week 2 entry. The master guide in `../CLAUDE.md` holds the workflow and playbook; this
file holds what is specific to this week. Read `specs/README.md` before touching anything.

**Product: Holdfast** (repo `Tanay3484/holdfast`). A safety net for webhooks: it sits between a provider
(GitHub, Stripe) and the user's app, stores every event before acknowledging it, forwards with retries,
holds what the app fails on, diagnoses why, and resends safely. Code does the checks and decides replay;
Gemma names the cause and writes an incident note a non-technical client can read. The customer is a
freelance developer or small agency maintaining client integrations.

Phase 0 is in review: `specs/000-brief/brief.md` is `Status: Draft`. The repo holds scaffolding only. No
feature code until the brief, requirements, design and tasks are approved.

Dropped on 2026-10-13, do not propose again: "Bill Check-up" (cloud bill anomaly finder). Tanay has no
personal cloud account with spend history, so it could only be proven on generated data.

Constraints from Tanay (2026-10-13):

- **Testable with Tanay's own real data.** Real webhooks from Tanay's own GitHub repos and Stripe test
  account. The evaluation must not depend on finding an outside user.
- **No message broker.** The store-and-forward logic is hand-written; that is the point of the project.
- **Non-technical readers come first.** The web UI must be beautiful and understandable by a client with no
  technical background: verdict first, jargon explained, a live demo one click away.
- **Go.**

## Repo

- `main` is protected, matching `order-taker`: pull request with 1 approval, stale approvals dismissed,
  conversations resolved, the `tests` check passing and up to date, no force pushes or deletions. Admins
  are not enforced, so Tanay can merge their own PRs.
- The CI job must stay named `tests`; the protection rule looks for that name.
- Work on a branch and open a PR. Never push to `main`.

## The challenge

- Page: <https://dev.to/challenges/hacktoberfest-week2-2026-10-12>
- Prompt: build something with open-weight models or open-source AI **that someone would pay for**. Small,
  specific, useful to a real person.
- Bonus points: put it in front of a real first user and report whether they paid or would pay, and what
  they asked for next.
- Due **2026-10-18 23:59 PDT** (2026-10-19 06:59 UTC). Winners the week of 2026-10-19.
- Judging, heaviest first: writing quality, relevance to theme, creativity, technical execution, partner tech.
- Rules: new project built inside the window, English post, 18+, one win per challenge.

## Prize categories

Overall $250. Featured $200: Render, TabPFN, Tinker, Arduino, DigitalOcean, Gemma. Partner $100: Backboard,
ElevenLabs, Entire, GitHub Copilot, Mastra, MongoDB Atlas, Sentry Agent Tracing, SerpApi, Temporal,
Tiger Data, .tech Domain (new this week).

- **Render**: Render as the AI runtime or agent front end. Hosting is already decided, so this is always in.
- **TabPFN**: forecast, predict, classify or spot anomalies in tabular data; they favour TabPFN embedded in
  a wider prediction agent. TabPFN-3.5 needs a free **non-commercial** licence from platform.priorlabs.ai;
  the v2 weights download without an account. The licence matters for a "would pay" product: decide in
  design and say so in the post.
- **Gemma**: run it locally, fine-tune it, or serve it from any provider.
- **.tech Domain**: host on a free .tech domain from hacktoberfest.com/my/promos and link it.
- **Sentry Agent Tracing**: show latency, tokens, cost, and a bug the traces found, with screenshots.

## Workflow

- Never write feature code for a spec whose `tasks.md` is not `Status: Approved`.
- Draft one phase at a time, then stop and ask for review.
- Tasks marked `@human` are Tanay's. Don't implement them; help only when asked.

## Commands

- Test / vet / format check: `go test ./...` · `go vet ./...` · `gofmt -l .` (must print nothing)
- Run and evaluation commands: to be added when the design is approved.

## Conventions

- Go 1.27. Binaries in `cmd/<name>/`, packages in `internal/`, tests beside the code as `_test.go`.
- Config from environment variables with one prefix, read in one package.
- The model and outbound HTTP sit behind small interfaces that tests fake. No network in tests.
- Captured payloads never go in the repo; committed fixtures are synthetic or scrubbed.
- The evaluation script is part of the product: it lives in `scripts/`, runs from one command, and writes
  the numbers the post quotes. Never hand-copy a number into the draft.
- Post draft in `drafts/devto-submission.md`.
