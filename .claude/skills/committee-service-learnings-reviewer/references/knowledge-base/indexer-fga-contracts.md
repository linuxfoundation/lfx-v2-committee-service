<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Indexer and FGA emission patterns

Local empirical patterns for what this service publishes on `lfx.index.*` and
`lfx.fga-sync.*`, and for the contract docs that describe it.

**Read when** the patch touches `internal/service/*writer.go`,
`internal/infrastructure/nats/messaging_publish.go`,
`pkg/constants/subjects.go`, `docs/indexer-contract.md`,
`docs/fga-contract.md`, or `scripts/migrations/**`.

---

## `indexer-fga-contracts/generic-publisher-bypasses-transport-invariant` — High

**Pattern:** a per-subject transport rule — this subject is async-only, that one
is always sync — is enforced in the named wrapper method but not in the generic
subject-taking method beside it. Any caller that passes the subject directly to
the generic method bypasses the rule entirely, and the wrapper's guard reads
like protection it is not providing.

**Detect:** when `docs/fga-contract.md` documents a transport invariant for a
subject, require the guard inside the generic publisher entry point —
`(*messagePublisher).Access` in
`internal/infrastructure/nats/messaging_publish.go:128-133` — and not only in
the named `UpdateAccess`/`DeleteAccess` wrappers. A new subject with a
documented transport invariant and no `Access` guard is the finding.

**Evidence:** `copilot-pull-request-reviewer`, PR #160, thread `r3658684265`,
on `internal/infrastructure/nats/messaging_publish.go:130`: *"The
asynchronous-only invariant is still bypassable through
`CommitteePublisher.Access`: it accepts any subject, so a caller can pass
`GenericUpdateAccessSubject` with `sync=true` and still reach `requestMessage`.
Guard that subject in `Access` by delegating to `UpdateAccess` regardless of
`sync`."*

Fixed in `35dcb5c` with `TestMessagePublisher_AccessGuardsUpdateAccessSubject`
asserting `client.requested == 0`.

**Recurrence, confirmed since the mining pass:** at `ec86a8f` this rule was
guarded for `update_access` only, with PR #162 still open applying the same
invariant to `delete_access`. That PR has since merged. At `main@bd39fe9`
`Access` guards **both** subjects
(`internal/infrastructure/nats/messaging_publish.go:128-133`), with
`TestMessagePublisher_AccessGuardsDeleteAccessSubject` alongside the original
(`internal/infrastructure/nats/messaging_publish_test.go:110` and `:133`). The
team extending the same guard to a second subject is what promotes this from a
one-off fix to a pattern.

**Failure message:** Transport invariant enforced only in the named wrapper —
a caller passing this subject to the generic `Access` method bypasses it.

**Fix:** guard the subject inside `Access` by delegating to the named method
regardless of `sync`, and add a test asserting the request path was not taken.

---

## `indexer-fga-contracts/contract-doc-internal-consistency-and-delivery-claims` — High

**Pattern:** two shapes of contract-doc defect that survive a normal same-PR
doc update. First, a doc is updated in one section while another section of the
*same* doc still states the thing the change falsified. Second, a doc asserts a
delivery guarantee stronger than the transport actually provides.

This entry **extends** the legacy `contract-doc-out-of-sync` rule rather than
replacing it. Match the legacy entry for plain code-vs-doc drift; match this one
for a doc that contradicts itself, or that overstates delivery.

**Detect:** when the patch edits a section of `docs/fga-contract.md`,
`docs/indexer-contract.md`, or `docs/invite-application-flows.md`, sweep the
rest of that same file for statements the edit made untrue — prose against a
trigger table, one flow's description against another's. Separately, flag any
doc sentence claiming a core-NATS publish is acknowledged, confirmed, or
delivered: `nats.Conn.PublishMsg` returns after enqueuing to the client buffer.

**Evidence:** `copilot-pull-request-reviewer`.

- PR #156, thread `r3632518923`: *"The updated identity-resolution contract is
  still internally inconsistent: the invite flow at line 80 … and the
  application flow at line 124 … Both can now use this JWT fallback."* Fixed.
- PR #160, thread `r3658684364`: prose contradicted the trigger table two
  sections below in the same document. Fixed in `35dcb5c`.
- PR #160, thread `r3658684324`: *"`nats.Conn.PublishMsg` has no broker
  acknowledgement and may only enqueue data in the client buffer; without
  `Flush`, a nil return does not mean core NATS accepted the message."* Fixed;
  verified in `main@bd39fe9` at `docs/fga-contract.md:39` — *"A successful core
  publish means only that the NATS client accepted the message for delivery (no
  immediate client-side error); it is not a broker acknowledgement."*

**Live violation to be aware of:** `docs/fga-contract.md:113` and the trigger
table at `:189` still describe `member_remove` as delete-only and say updates
with an empty username are skipped, while
`internal/service/committee_member_writer.go:1254` emits `member_remove` when
an update clears the username. Copilot raised exactly this on PR #161 and the
author deferred it. Because the fix was deferred rather than made, this is
evidence that the legacy `contract-doc-out-of-sync` entry is still live — not a
separate pattern.

**Failure message:** Contract doc is internally inconsistent after this change,
or claims a delivery guarantee the transport does not provide.

**Fix:** after editing one section, re-read the whole document for statements
the edit falsified — trigger tables especially — and describe core-NATS
publishes as accepted-for-delivery, not acknowledged.
