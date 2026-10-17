# 001 Holdfast MVP — Tasks

Status: Draft
Owner: Tanay
Updated: 2026-10-14

Built ahead of approval at Tanay's request on 2026-10-14: the `@ai` setup tasks below are done; the `@human`
levels and the evidence work are open. Due 2026-10-18 23:59 PDT.

## Your levels (@human)

Each one is a single file with the stub, the contract and the known answers in its comments. Write it by
hand, add your own `_test.go` beside it, then run `go run ./cmd/unlock` and restart the server to see the
site change. Any order works; this order unlocks the demo in the most satisfying way.

- [x] **H1 · Signature check** — `internal/verify/verify.go` · @human · R1.5, R1.6, R5.2 · ~1.5 h
  - 2026-10-18: `GitHub` written by Tanay. `Stripe` started by Tanay (header check, loop structure) and
    finished by the AI at Tanay's request; tests by the AI.
  - In: secret, raw body, header value (Stripe also: now, tolerance). Out: nil or one of the four errors.
  - Edge cases: empty header; missing `sha256=`; non-hex; several `v1` values (rotation); `v0` present;
    timestamp in the future beyond tolerance; check the signature before the timestamp.
  - Done when: level 1 shows 10/10, and the "Signed with the wrong key" demo is held instead of delivered.
  - Hints: `crypto/hmac`, `crypto/sha256`, `encoding/hex`, `hmac.Equal`, `strings.Split`, `strconv.ParseInt`.

- [x] **H2 · Smart retries** — `internal/policy/policy.go` · @human · R2.2 · ~1 h
  - 2026-10-18: written by Tanay (all 12 checks passed). At Tanay's request the AI polished it ("the app"
    instead of "the shop", one cap check, readable waits) and added `policy_test.go`.
  - In: attempt number (1-based), outcome. Out: Delivered, Retry with a wait, or Hold, and a plain reason.
  - Edge cases: attempt equal to MaxAttempts; Retry-After larger than MaxDelay; 3xx; 408/429; overflow
    when attempt is large (cap before you multiply).
  - Done when: 12/12, and "The shop goes down for a few seconds" is delivered on a later try.

- [x] **H3 · What changed** — `internal/shape/shape.go` · @human · R3.1 · ~2.5 h
  - 2026-10-18: written by the AI at Tanay's request, as a worked example of recursion (Tanay wrote the
    JSON decoding and started `walk`). H1, H2 and H4 stay Tanay's.
  - In: a JSON body; two shapes. Out: path → kind map; sorted changes.
  - Edge cases: empty array; arrays of arrays (`$.a[][]`); items that disagree (Mixed); null; top-level
    array; invalid JSON; keys containing dots (leave as they are).
  - Done when: 7/7, and "The payment service renames a field" shows `customer_id removed` and is
    diagnosed as "The data changed shape".

- [x] **H4 · Safe resend** — `internal/replay/replay.go` · @human · R1.7, R5.1–R5.3 · ~1.5 h
  - 2026-10-18: written by the AI at Tanay's request (late night before the deadline), with tests.
  - In: source, headers, body; held events and delivered keys. Out: key; one step per held event.
  - Edge cases: Stripe body that is not JSON; empty key; duplicate of an already-delivered key; equal
    ReceivedAt (tie-break on ID); do not sort the caller's slice in place.
  - Done when: 10/10, "The same payment is sent twice" is recorded once by the shop, and "Send all held
    again" works.

## Setup (@ai, done 2026-10-14)

- [x] **T1** Unlock game: levels, checks, board, `cmd/unlock` · R9.1, R9.3
- [x] **T2** Stubs and contracts for H1–H4, `wip.ErrLocked` · R9.1
- [x] **T3** SQLite store with durable insert, attempts, requeue, prune · R1.1, R2.4, R11.1
- [x] **T4** Relay receive: route lookup, size limit, signature, dedupe, store-then-ack · R1.1–R1.8
- [x] **T5** Delivery loop with fallback policy, graceful shutdown, no redirects · R2.1–R2.6
- [x] **T6** Diagnosis: rules, prompt, schema, checks, fallback, restart catch-up · R4.1–R4.6
- [x] **T7** Client note: template, model rewording with number check · R6.1, R6.2
- [x] **T8** Resend through the plan, refusal while locked · R5.1–R5.4
- [x] **T9** Website: home, live dashboard, event page, progress board, private routes, light and dark,
  phone layout, WIP tiles · R7.1–R7.6, R9.2, R9.4
- [x] **T10** Demo shop, fault buttons, rate limit · R8.1–R8.3
- [x] **T11** Dockerfile, Ollama image, `render.yaml`, `.env.example` · design §8
- [x] **T12** Tests for store, relay, diagnose, unlock, describe, web (pass with levels locked or not)
- [x] **T13** Subagents in `.claude/agents/`

## Evidence and launch (open)

- [ ] **T14** Capture real webhooks (GitHub repo webhooks, Stripe test mode) and export them to
  `data/captured/` · @human+ai · R10.1 · needs H1 for signed capture
- [ ] **T15** `scripts/eval`: fault injection on captured events, three modes, held-out split,
  `results/eval.md` · @ai · R10.1
- [ ] **T16** `scripts/crashtest`: kill app and relay mid-stream, count lost and repeated · @ai · R10.2
- [ ] **T17** Deploy to Render; decide design §10 Q1 (share Week 1's Ollama) · @human+ai
- [ ] **T18** Claim the .tech domain and point it at the web service · @human
- [ ] **T19** First user: Tanay runs a real GitHub webhook through the deployed relay; an outside
  freelancer if one turns up · @human
- [ ] **T20** Draft the DEV post in `drafts/devto-submission.md` from `results/eval.md` · @ai (post-writer)
- [ ] **T21** Optional: Sentry agent tracing on model calls · @ai · cut first

## Changelog

- 2026-10-14: First draft. T1–T13 built the same day at Tanay's request.
