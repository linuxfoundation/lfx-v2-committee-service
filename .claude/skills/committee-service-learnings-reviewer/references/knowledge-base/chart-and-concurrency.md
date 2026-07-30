<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Wiring and deployment-shape patterns

Local empirical patterns for the code paths where something compiles, passes
tests, and still does nothing in a deployed environment.

**Read when** the patch touches `charts/lfx-v2-committee-service/**`,
`pkg/constants/subjects.go`, `cmd/committee-api/service/committee_handler.go`,
`cmd/committee-api/service/providers.go`, or `cmd/committee-api/design/**`.

---

## `chart-and-concurrency/handler-registered-but-not-subscribed` — Critical

**Pattern:** a new inbound NATS subject gets a constant and a dispatch case in
the message handler, but is never added to the subscription map. The handler is
reachable in unit tests, which call it directly, and unreachable in every
deployed environment, which only ever delivers what the service subscribed to.

**Detect:** the diff adds an inbound subject constant to
`pkg/constants/subjects.go` **and** a dispatch case in
`cmd/committee-api/service/committee_handler.go`. Require that same subject to
appear in the `QueueSubscriptions` map in
`cmd/committee-api/service/providers.go` (see the block around `:915`, where
every live inbound subject is mapped to a handler). A dispatch case with no
subscription entry is the finding.

Do not extend this to the `references/nats-messaging.md` inventory update.
That obligation is quarantined pending a human decision — see the README.

**Evidence:** `copilot-pull-request-reviewer`, PR #161, thread `r3659193428`
(2026-07-27T16:49:00Z), on
`cmd/committee-api/service/committee_handler.go:43`: *"Registering the
dispatcher here is insufficient: `QueueSubscriptions` only subscribes to the
subjects listed in `cmd/committee-api/service/providers.go:899-913`, and this
new subject is absent there. Consequently, deployed instances never receive
these deletion events."*

Fixed in `ceab5a1` at 17:10:19Z — 21 minutes after the finding — which added
`constants.V1SyncHelperUserDeletedSubject: messageHandlerService.HandleMessage`
to the map. Verified in `main@bd39fe9` at
`cmd/committee-api/service/providers.go:915`.

**Provenance caveat, recorded deliberately:** the developer's thread reply at
18:29Z read *"No longer applicable — registered in providers.go:913"*. That was
true only because his own review-response commit had just added it. The finding
was real and was acted on; the reply's framing is not supported by the history.
A reply is not fix evidence in either direction — check the commit.

**Why it earns a place:** cost of miss is total and silent. The code compiles,
the unit tests pass because they invoke the handler directly, and the event is
never delivered anywhere it matters. No test in this repo's current shape can
catch it.

**Failure message:** New inbound subject dispatched in the handler but absent
from `QueueSubscriptions` — deployed instances will never receive these events.

**Fix:** add the subject to the `QueueSubscriptions` map in
`cmd/committee-api/service/providers.go` in the same change as the dispatch
case.
