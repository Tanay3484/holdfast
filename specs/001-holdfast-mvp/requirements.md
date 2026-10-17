# 001 Holdfast MVP — Requirements

Status: Draft
Owner: Tanay
Updated: 2026-10-14

Built ahead of approval at Tanay's request on 2026-10-14 (setup day before TOEFL prep). Review this file,
`design.md` and `tasks.md` together; anything you change here becomes a spec edit, then a code change.

Brief: [../000-brief/brief.md](../000-brief/brief.md)

## R1 Receiving

- **R1.1** WHEN a POST arrives at `/in/{route}` for a configured route, the system SHALL store the raw body
  and headers durably before answering 200.
- **R1.2** IF the route is not configured, THEN the system SHALL answer 404 and store nothing.
- **R1.3** IF the body is larger than 1 MiB, THEN the system SHALL answer 413 and store nothing.
- **R1.4** IF storing fails, THEN the system SHALL answer 503 so the provider retries.
- **R1.5** WHEN a route has a secret and the signature check is written, the system SHALL record the result
  as valid, invalid, missing, malformed or stale.
- **R1.6** IF the signature result is invalid, missing, malformed or stale, THEN the system SHALL hold the
  event and never forward it.
- **R1.7** WHEN an event's dedupe key matches an event already stored for the route, the system SHALL mark
  it as a duplicate and not forward it.
- **R1.8** The system SHALL drop Authorization, Cookie and Proxy-Authorization headers before storing.

## R2 Delivering

- **R2.1** The system SHALL forward the original body, the provider's own headers, `Holdfast-Event-Id` and
  `Holdfast-Attempt` to the route's destination.
- **R2.2** WHEN an attempt ends, the system SHALL record it and apply the retry policy: delivered, retry
  after a wait, or hold.
- **R2.3** WHILE the retry policy is not written, the system SHALL try once and hold on any failure.
- **R2.4** WHEN the process restarts, the system SHALL resume every pending or retrying event.
- **R2.5** The system SHALL let an attempt in flight finish when shutting down, and record it.
- **R2.6** The system SHALL not follow redirects; a redirect counts as an answer.

## R3 Comparing

- **R3.1** WHEN an event is held after the app answered, the system SHALL compare its JSON shape with the
  last delivered event of the same route and type, if there is one.

## R4 Diagnosing

- **R4.1** WHEN an event is held, the system SHALL produce a diagnosis: a cause from the fixed list, a
  plain summary for the client and a fix for the developer.
- **R4.2** The system SHALL compute a rule-based cause for every held event and pass it to the model.
- **R4.3** IF the model fails, answers with a cause not on the list, or times out, THEN the system SHALL use
  the rule-based diagnosis and say so on the page.
- **R4.4** IF the model's client summary contains technical words, THEN the system SHALL replace it with
  the rule-based summary for the model's cause.
- **R4.5** The system SHALL never show a confidence score.
- **R4.6** WHEN the server starts, the system SHALL diagnose held events that have no diagnosis.
- **R4.7** The client summary SHALL say what happened to this message (who and how much, when known), why
  in everyday words, and what happens next, without naming fields.
- **R4.8** IF the model's client summary claims anyone is reviewing, fixing or investigating the problem,
  THEN the system SHALL replace it with the rule-based summary for the model's cause.
- **R4.10** WHILE the model is still writing, the event page SHALL show the rule-based diagnosis marked
  as a first answer, and replace it when the model's arrives.
- **R4.9** WHEN a removed field and an added field read the same apart from punctuation and case, the
  system SHALL report them to the model as one rename, from the old path to the new one.

## R5 Resending

- **R5.1** WHEN a person resends one held event or all of a route's held events, the system SHALL plan the
  resend and queue only the events the plan marks to send, in arrival order.
- **R5.2** The system SHALL never resend an event whose signature check failed.
- **R5.3** The system SHALL never resend an event whose dedupe key was already delivered, or send two copies
  with the same key.
- **R5.4** WHILE the resend plan is not written, the system SHALL refuse resends and say why.

## R6 Client note

- **R6.1** WHILE a route has held events, the dashboard SHALL show a note for the client with the count,
  the time window, what kinds of events they are and what went wrong.
- **R6.2** The system SHALL let the model reword the note only if every number in its version appears in
  the code-written note and it contains no technical words.

## R7 Website

- **R7.1** The first thing on a dashboard SHALL be a verdict: everything arrived, delivering, or N held.
- **R7.2** Each event SHALL have a page with what happened, whether it is genuine, each try, what changed,
  a resend button, a section for the developer and the raw message.
- **R7.3** Pages SHALL read correctly on a 360 px wide phone and in light and dark mode.
- **R7.4** Dashboards SHALL refresh on their own while open.
- **R7.5** Routes not marked public SHALL need the admin password (HTTP basic auth, any user name).
- **R7.6** Form posts from other sites SHALL be refused.

## R8 Live demo

- **R8.1** The home page SHALL let anyone trigger each fault in `faults.Demo` against a demo shop, through
  the real `/in/` endpoint.
- **R8.2** The demo shop SHALL record each payment it accepts and flag one it has already recorded.
- **R8.3** The system SHALL limit each visitor to one demo action every two seconds.

## R9 Unlock game

- **R9.1** Each `@human` package SHALL be a level with known-answer checks; a level is locked, cracked or
  unlocked.
- **R9.2** WHILE a level is not unlocked, every part of the site it powers SHALL show a WIP tile naming the
  file to write.
- **R9.3** `go run ./cmd/unlock` and `/progress` SHALL show the same board.
- **R9.4** WHEN every level is unlocked, the progress link SHALL disappear from the menu.

## R10 Evidence

- **R10.1** One command SHALL run the fault-injection evaluation on captured real webhooks and print the
  right-cause rate for rules alone, the model alone and both together, with every miss.
- **R10.2** One command SHALL run the crash test and print events lost and events forwarded twice.

## R11 Housekeeping

- **R11.1** The system SHALL delete events older than `HF_RETENTION` (default 72 h).

## Non-goals

As in the brief. Also: no accounts, no per-visitor demo isolation, no HTML email.

## Changelog

- 2026-10-14: First draft, written alongside the build.
- 2026-10-18: Added R4.7–R4.9 after Tanay found Gemma's summary vague ("being kept safe and reviewed")
  and its fix backwards (it said the app newly required `customer_id`).
