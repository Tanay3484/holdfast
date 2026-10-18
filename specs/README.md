# Specs — AI-PDLC workflow

Every feature goes through gated phases. An AI agent may draft any phase, but a **human approves each gate**
before the next phase starts. Approval is recorded by setting `Status: Approved` in the file header.

| Phase | File | Answers | Gate |
|-------|------|---------|------|
| 0. Brief | `000-brief/brief.md` | *Who pays and why*: the user, the claim we will measure, prize categories | Human picks the product |
| 1. Requirements | `requirements.md` | *What* and *why*: user stories, acceptance criteria (EARS), non-goals | Human approves scope |
| 2. Design | `design.md` | *How*: architecture, data flow, interfaces, prompts, risks | Human approves approach |
| 3. Tasks | `tasks.md` | Ordered, small, independently verifiable tasks traced to requirements | Human approves plan |
| 4. Implement & Verify | code + tests | Each task: implement → test → tick checkbox → commit | Tests green, AC demoed |
| 5. Evidence & Post | `scripts/` + `drafts/` | Evaluation run, first-user session, write-up | Human publishes |

Phase 0 happens once per project. Phases 1–4 repeat per feature in its own numbered folder.

## The brief

`000-brief/brief.md` is one page and contains:

- **User**: one named real person, what they do, and the moment the product is for.
- **Would they pay**: what it saves or earns them, in money or hours.
- **Evidence plan**: the one measurable claim, the dataset, the baseline it must beat, the held-out split.
- **First user**: who will try it, when, and what we will ask them.
- **Prize categories**: each one entered and the build decision that justifies it.
- **Non-goals** and the cut line if time runs short.

## Rules

- **Traceability.** Every task cites requirement IDs (e.g. `R2.1`); every acceptance criterion is covered by a
  test or a documented manual check.
- **Specs are the source of truth.** If implementation reveals a spec is wrong, update the spec first (and
  note it in the Changelog), then the code.
- **Small steps.** One task ≈ one commit ≈ one reviewable diff.
- **Open questions block their gate.** A phase can't be approved while it has unresolved open questions.
- **Task ownership.** Every task in `tasks.md` has an owner: `@human` or `@ai`. The AI never implements
  `@human` tasks. It may write the failing tests / interface stubs for them only if the owner asks, and it
  reviews them when asked. Pairing tasks are marked `@human+ai`.
- **`@human` task format.** Each one states its inputs, outputs, edge cases and the check that proves it
  done, and is ordered so the app runs on a stub until it lands.
- **Numbers come from the script.** Any figure in the post is produced by a command in `scripts/` and can be
  reproduced by a reader.

## Acceptance-criteria format (EARS)

- Ubiquitous: `The system SHALL <response>.`
- Event: `WHEN <trigger>, the system SHALL <response>.`
- State: `WHILE <state>, the system SHALL <response>.`
- Unwanted: `IF <condition>, THEN the system SHALL <response>.`

## Index

| ID | Feature | Phase | Status |
|----|---------|-------|--------|
| 000 | [Brief: Holdfast](000-brief/brief.md) | 0 | Approved |
| 001 | [Holdfast MVP](001-holdfast-mvp/) | 4 | Draft; H1–H4 done 2026-10-18; evidence tasks T14–T21 open |
| 002 | [Demo and Live modes](002-demo-and-live/requirements.md) | 4 | Draft; built 2026-10-18 at Tanay's request; T27–T28 open |
