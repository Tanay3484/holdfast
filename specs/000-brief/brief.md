# Brief — Holdfast

Status: Draft
Owner: Tanay
Updated: 2026-10-13

## Product

A safety net for webhooks. Holdfast sits between a provider (GitHub, Stripe) and your app. It stores every
event before acknowledging it, forwards it, and when your app fails it holds the event, works out why, and
resends it safely once the problem is fixed.

## User

- **Who pays:** a freelance developer or small agency that looks after integrations for a handful of
  clients. Keeping those integrations alive is the side hustle; Holdfast is the on-call they do not have.
- **Who reads the page:** the developer, and the client they forward it to, who is not technical.
- **The moment:** a client messages "orders stopped showing up" and the developer has to find out what was
  lost, why, and whether it can be recovered.

## Would they pay

A lost webhook is a lost order or an unrecorded payment. Closed tools that only capture and replay
webhooks already charge a monthly fee. Holdfast adds two things they do not: nothing is lost in the first
place, and the explanation is written for the client.

## How it works

1. **Store, then acknowledge.** An incoming webhook is written to disk before the provider is told it
   arrived.
2. **Forward with retries.** A worker delivers it to the app, backing off on failure. After a set number
   of failures the event is held.
3. **Code checks.** For a held event, code recomputes the signature, validates the body, and compares the
   payload's shape with the last successful event of the same type.
4. **The model diagnoses.** Gemma reads the request, the app's error response and the code checks, and
   names the likely cause and the fix. It is never asked for a confidence number.
5. **The model writes the incident note** in plain language, from counts and times that code supplies.
6. **Replay is decided by code.** Resending uses a duplicate guard so nothing is delivered twice.

There is no message broker. The store-and-forward logic is the product, written by hand.

## Evidence plan

- **Claim 1, diagnosis:** on real provider webhooks with known faults injected, code checks plus the model
  name the right cause more often than code checks alone.
- **Claim 2, safety:** when the app is killed mid-stream, no event is lost and none is delivered twice.
- **Data:** real webhooks sent by GitHub and by Stripe's test mode to Tanay's own accounts. No outside user
  is needed.
- **Faults injected:** wrong signing secret, body altered before verification, stale timestamp, renamed or
  missing field, slow handler, duplicate delivery.
- **Baseline:** code checks alone, and the model alone, each scored on the same set.
- **Held out:** at least one fault type and one event type never seen while writing prompts or rules.
- **Reported:** right-cause rate for each of the three setups, the misses, and lost and duplicated counts
  from the crash test, all from one command in `scripts/`.

## The page must work for a non-technical reader

- The first screen is a timeline with a verdict: "everything arrived", or "3 events are held, here is why".
- Each held event is one card: what happened, what it affects, what to do, whether it is safe to resend.
- No jargon without a one-line explanation beside it.
- It looks finished and reads well on a phone.
- A demo is one click away: a button sends a deliberately broken sample event through the live relay, so
  a judge sees hold, diagnosis and resend without any account.

## First user

Tanay, with real webhooks from their own GitHub repos and Stripe test account. A freelance developer
outside the project tries it before the post if one can be found; the post says which happened.

## Prize categories

| Category | Build decision that justifies it |
|----------|----------------------------------|
| Render | Relay, store and model runtime are deployed there from one Blueprint |
| Gemma | Writes every diagnosis and incident note |
| .tech Domain | The relay address providers send to is on it |
| Sentry Agent Tracing | Only if model latency and token traces are wired in and find something worth showing |

## Decisions

- **Language:** Go.
- **Providers:** GitHub and Stripe test mode, plus a generic unsigned webhook.
- **Name:** Holdfast (repo `Tanay3484/holdfast`).
- **Captured payloads stay out of the repo.** Fixtures committed here are synthetic or scrubbed.
- **Storage, model size and hosting cost** are settled in design.

## Non-goals

- A general-purpose queue or event bus.
- A local tunnel or request inspector for development.
- Changing the user's app or its code.
- Accounts, teams, billing.
- Providers beyond the two named.

## Cut line

If time runs short, drop in this order: Sentry tracing, .tech domain, Stripe (GitHub only), the model-alone
baseline. The crash test and the fault-injection run are never cut.

## Open questions

None.

## Changelog

- 2026-10-13: First draft after Tanay picked the webhook safety net.
