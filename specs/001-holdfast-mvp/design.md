# 001 Holdfast MVP — Design

Status: Draft
Owner: Tanay
Updated: 2026-10-14

Built ahead of approval at Tanay's request on 2026-10-14. This describes the code as it is.

## 1. Shape

One Go binary (`cmd/holdfast`) runs everything: the public `/in/` endpoint, the delivery worker, the
diagnosis worker, the website and the demo shop. A second service runs Ollama with Gemma.

```
provider ──POST /in/{route}──▶ relay.Receive ── verify (H1) ── dedupe key (H4) ──▶ SQLite (commit) ──▶ 200
                                                                                   │
                         ┌─────────────────────────────────────────────────────────┘
                         ▼
                 deliver loop ── POST ──▶ app ──▶ policy.Decide (H2) ──▶ delivered │ retry at t │ held
                                                                                               │
                 diagnose loop ◀───────────────────────────────────────────────────────────────┘
                   facts: signature, last answer, shape.Diff vs last delivered (H3)
                   rules ──▶ Gemma (fixed causes, JSON schema) ──▶ code checks ──▶ diagnosis + client note

                 resend: replay.Plan (H4) ──▶ requeue the ones to send
```

## 2. Packages

| Package | Owner | Job |
|---------|-------|-----|
| `verify` | @human H1 | GitHub and Stripe signature checks |
| `policy` | @human H2 | Delivered / retry / hold after each attempt |
| `shape` | @human H3 | JSON shape of a body, diff of two shapes |
| `replay` | @human H4 | Dedupe key, resend plan |
| `wip` | @ai | `ErrLocked` returned by stubs |
| `unlock` | @ai | Known-answer checks per level, board |
| `store` | @ai | SQLite: events, attempts |
| `relay` | @ai | Receive, deliver, diagnose, resend, client note |
| `diagnose` | @ai | Rules, model prompt and checks, client note |
| `ollama` | @ai | Chat client with JSON-schema output |
| `describe` | @ai | "Payment received · ₹450 · Asha" from a raw body |
| `faults` | @ai | Faulty webhooks for the demo and the evaluation |
| `demo` | @ai | Chai & Co. shop and demo runner |
| `web` | @ai | Pages, fragments, actions; templates and CSS embedded |
| `config` | @ai | `HF_` environment variables |

Every call into an @human package goes through `relay.guard`, which turns a panic into an error. A locked
or panicking function makes the relay fall back (R2.3, R5.4); it never takes the server down.

## 3. Storage

SQLite through `modernc.org/sqlite` (pure Go, so no C compiler on Windows or in the image). WAL with
`synchronous=FULL`, one connection. `events` holds the raw request and current status; `attempts` holds
every try. An `INSERT` commits before `/in/` answers 200, which is the whole durability promise (R1.1).

Statuses: `pending → delivered`, `pending → retrying → … → delivered | held`, `held → pending` (resend),
`duplicate` (terminal). A mutex makes the duplicate check and the insert one step.

## 4. Delivery

One worker, polling every 250 ms and woken early by new events. It takes due events oldest first and does
not hold later events back while an earlier one waits to retry; providers do not promise order either.
Each attempt has the route's timeout (10 s real, 3 s demo) and runs on a context that shutdown does not
cancel, so a request in flight is finished and recorded (R2.5).

Delivery is at least once. If the app processes a request and the answer is lost, the next attempt sends
the same event again with the same `Holdfast-Event-Id`, which the app can use to ignore repeats.

## 5. Diagnosis

`Rules(facts)` gives a cause from signature result, last answer and shape changes. In `Combined` mode
(the product) the model sees the facts plus the rules' cause and picks from the same eight causes under a
JSON schema. Code then checks: cause on the list (else rules), summary free of jargon and under 400
characters (else the rules' summary for the model's cause), fix not empty. Titles always come from code.
`RulesOnly` and `ModelOnly` exist for the evaluation (R10.1).

Measured on 2026-10-18 with gemma3:4b: given a raw list of changed paths, Gemma read the renamed field
backwards ("the app now requires customer_id") and wrote a vague summary that promised a review nobody
was doing. Fix: code detects renames (`Renames`) and describes every change as a sentence (`Explain`),
saying the sender changed and the app did not; the prompt now asks for three things in the summary
(what happened to this message, why, what next) with one example from a different case; and a code
check (`Promise`) rejects promised actions. The renamed-field diagnosis then named the customer and
amount, explained the change in everyday words and pointed the developer at `data.object.customer.id`.
Payment lines now read "₹550 from Dev" after Gemma once wrote "Dev received a ₹550 payment".

The client note is written by code (`TemplateNote`) and may be reworded by the model only if every digit
sequence in the model's version appears in code's version (R6.2). It is cached per set of held events.

Measured on 2026-10-14 with gemma3:4b locally: without level 3, a renamed field was diagnosed as "the app
turned it away" from the app's error text alone. The first version of the prompt let the model write
"the issue has been identified", which is not a fact; the prompt now forbids promises. Both are material
for the post.

## 6. Website

Server-rendered `html/template` with a little vanilla JS for refresh, background form posts and copy
buttons; the site works without JS. Fonts from Google Fonts; everything else embedded in the binary.
Dashboards poll an HTML fragment every 2 s (`/fragments/board/{route}`, `/fragments/event/{id}`). Event
pages stop polling once the event is final.

Public routes (the demo) are visible to anyone; others need basic auth with `HF_ADMIN_TOKEN` (R7.5).
POSTs check `Sec-Fetch-Site` or `Origin` (R7.6).

## 7. The unlock game

Each level is a list of known-answer checks (`internal/unlock/levels.go`) that call the real @human
function. All checks pass: unlocked. All return `ErrLocked`: locked. Anything else: cracked, and the first
failing check is shown as a hint. The board is computed once at start-up, because code cannot change while
the server runs. Templates ask `unlocked "retries"` and render `{{template "wip" (level "retries")}}`
otherwise, so each level reveals its panels on the event page, its step in "How it works", and the
behaviour of its demo buttons.

## 8. Hosting

`render.yaml`: the `holdfast` web service (Docker, 0.5 CPU / 512 MB, 1 GB disk at `/var/data`) and the
`holdfast-ollama` private service (1 CPU / 2 GB, gemma3:1b baked in), Singapore. Real routes go in
`HF_ROUTES` in the dashboard; `HF_ADMIN_TOKEN` is generated.

## 9. Evaluation (to build, T14 and T15)

- **Capture:** real webhooks from Tanay's GitHub repos and Stripe test mode (`stripe listen --forward-to
  localhost:8080/in/stripe-test` then `stripe trigger …`) are stored by Holdfast itself, then exported
  with headers and body to `data/captured/` (git-ignored).
- **Inject:** `faults` applies each fault to each captured event and re-signs where needed; the destination
  is a stub app that fails the way the fault implies.
- **Score:** right-cause rate per mode, held-out fault type and event type kept out of prompt tuning, every
  miss listed. Output to `results/eval.md`, which the post quotes.
- **Crash test:** send N events while killing and restarting the app, then the relay, and count events
  lost and events the app saw twice without a matching `Holdfast-Event-Id`.

## 10. Open questions

1. **Share Week 1's Ollama?** `golden-hour-ollama` already runs gemma3:1b in the same region. Pointing
   `HF_OLLAMA_URL` at it saves the 1c-2g service; the cost is that both projects share one slow CPU model.
   Recommendation: share, to protect the credit.
2. **Proposed brief edit.** Claim 2 says "none delivered twice". Delivery is at least once (§4): Holdfast
   never forwards a copy it recognises and never resends what was delivered, but an app that dies after
   processing a request can see it again with the same `Holdfast-Event-Id`. Proposed wording: "no event is
   lost, no duplicate is forwarded, and every repeat attempt carries the same event id."

## Changelog

- 2026-10-14: First draft, written alongside the build.
