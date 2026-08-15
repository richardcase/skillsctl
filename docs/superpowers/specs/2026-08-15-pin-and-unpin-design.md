# pin and unpin

Design of record for `skillsctl pin` and `skillsctl unpin`, which add or remove
a pin on a skill that is already installed. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which specifies `--pin`
as an install flag and says what every reader of a pin does with it.

Issue: [#9](https://github.com/richardcase/skillsctl/issues/9).

## Why

Every reader of a pin already exists. `update` skips a pinned skill unless it is
named, and naming one re-pins it at the new commit. `outdated` lists a pin and
marks it, so a pin never hides that something moved. `install --pin` records the
pin and leaves `Ref` empty.

What has never existed is a writer. `--pin` is an install flag, so changing your
mind means removing the skill and installing it again — under the same name,
into the same agents, with the same `--skill` and `--as` you used the first
time, all reconstructed by hand from `list`. The receipt already holds the one
bit that has to change.

## Pinning is a receipt-only mutation

`pin` freezes the revision that is already installed. It resolves nothing,
fetches nothing, extracts nothing and re-links nothing: the symlinks point at
the revision they already pointed at, and the store is untouched.

So the plan is a `Record` and nothing else — the shape `adopt` arrived at, and
for the same reason. It is what makes "this command cannot damage an install" a
property of the design rather than something a reviewer has to check by eye, and
it is what makes `--dry-run` exhaustive: every line it prints is a record, and
there is nothing else there to be.

## Decisions

**Two commands, not a flag on `update`.** `update --pin` would conflate moving
a skill with freezing it, and `update --no-pin` could not express the thing a
user most often wants — unpin *without* updating. `pin` and `unpin` are also
what someone reaches for first, and each help text says one thing.

**`pin` freezes what is installed.** It takes no `--ref`. Pinning at some other
revision is an update with a pin on the end: it fetches, extracts, re-links
every agent, needs the dirty check and the rollback and the gc hint, and none of
that belongs behind a verb that otherwise writes one field.

**A pinned receipt still records no `Ref`.** That invariant predates this work —
`install` clears the ref when pinning, and `Update` reads an empty ref as the
repository's default branch, which is what lets a named update of a pin resolve
against something. `pin` clears the ref for the same reason, and says which ref
it dropped so the loss is visible rather than silent.

**`unpin` resumes the default branch unless told otherwise.** A pin holds no ref
to restore, so `unpin <name>` leaves the receipt tracking the default branch and
`unpin <name> --ref develop` chooses one. The alternative — remembering the
pre-pin ref in a new receipt field — buys an exact round trip for the pins this
command created, and nothing at all for the pins `install --pin` and `adopt`
created, which never had a ref to remember.

**`--ref` is verified before it is recorded.** A ref that does not resolve turns
the next `update` or `outdated` into a failure the user cannot connect to the
command that caused it. Verification is one `git ls-remote` through the existing
`gitx.Resolve` — the same read-only round trip `outdated` makes, no fetch, no
extraction. `unpin` with no `--ref` makes no network call at all.

**Only the git channel can be pinned.** A local skill is whatever is in its
directory right now and there is nothing to freeze; a plugin's version is
whatever the agent decides. Both refuse, in the voice `rejectRevisionFlags` and
`rejectRepositoryFlags` already use for `--pin` at install time. The refusal
lives behind the `Channel` interface rather than as a switch on the receipt's
channel at the call site, which is the rule the whole package exists to keep.

### Unpinning what adopt pinned

`adopt` pins a skill it found in a git working copy deliberately: the pin is
what stops a plain `update` re-pointing that symlink out of the user's checkout
and into the store. `unpin` on such a receipt is allowed — it is the user asking
for exactly that takeover — but the output names the consequence, because it is
the one case where writing a receipt field changes what a later command does to
files the user owns. A receipt whose `RevPath` is outside the store is the test,
which `store.Contains` already answers.

## Surface

```
skillsctl pin <name>... [--dry-run]
skillsctl unpin <name>... [--ref R] [--dry-run]
```

```
$ skillsctl pin alpha
pinned alpha at 9f8e7d6 (it no longer tracks develop)

$ skillsctl unpin alpha
unpinned alpha; it now tracks the repository's default branch

$ skillsctl unpin alpha --ref develop
unpinned alpha; it now tracks develop
```

Variadic, like `update`, and an unknown name is reported in update's words.
Exit codes follow the shared table: everything asked for is 0, a mix is 2 with
the reasons printed, and nothing done is 1. Pinning something already pinned is
not a skip in that sense — nothing failed, exactly as a pin skipped by `update`
is not a failure.

## Structure

`Channel` gains one method. `PinOptions` says which way the pin moves and what
to track once it is released; `PinResult` carries the new receipt, whether
anything changed, and a note worth printing about a receipt that did.

```go
Pin(r state.Receipt, o PinOptions) (plan.Plan, PinResult, error)
```

`Git` implements it, `Local` and `Plugin` refuse. It is not on the shared
`linked` embed: git and local share how a skill reaches an agent, which is what
`linked` is for, and differ on whether it tracks anything, which is what this
is.

`internal/cli/pin.go` holds both commands over one runner, which differ in the
direction, the `--ref` flag and the verb they print. Ref verification lives
there rather than in the channel, since it is the only part of either command
that touches the network.

No state schema change: `Pinned` and `Ref` are already on the receipt.

## Deliberately not in scope

`pin --all`, and any form of `pin` that moves a skill to a different revision.
Retargeting the ref an unpinned skill tracks — `--ref` is honoured only on the
transition out of a pin, and a `set-ref` verb is a separate decision.
