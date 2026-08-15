# Interactive skill selection

Design of record for issue #48. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which specified that a
bare `install` on a multi-skill repository lists and stops; this decides what
happens when there is someone there to ask.

## Problem

`skillsctl install owner/repo` against a repository holding several skills
lists what it found and exits non-zero, telling the user to re-run with
`--skill <name>` or `--all`. Learning the names and then retyping the command
is two steps for what should be one. Issue #48 asks for what `npx skills
install` does: list the skills, and let the user pick from that list.

## Decisions

**An arrow-key checkbox picker.** `↑`/`↓` (or `k`/`j`) move, space ticks, `a`
ticks everything, enter confirms, `q` and Ctrl-C cancel. This is what the issue
asked for by name, and the alternative — a numbered line prompt — is cheaper to
build but is not the thing that was asked for.

**Escape sequences are reassembled across reads, and Esc waits.** A local
terminal writes `\x1b[B` in one go, but a pipe or an ssh link can split it, and
a lone escape byte read on its own cannot be told from the Esc key. Deciding
per-read makes a split arrow cancel the selection the user was scrolling
through — which is exactly what a pty test caught. So an escape byte with
nothing after it yet is carried into the next read rather than decoded, and Esc
only cancels once another key follows it. Arrows are pressed far more often
than Esc, and `q` and Ctrl-C cancel outright, so this trade falls the right way.
The alternative is a timer, which would put a clock inside the pure model.

**Without a terminal, nothing changes.** The listing and the exit code stand
exactly as they were. This is the load-bearing decision: an unattended
`install` must never install something nobody chose, and it keeps the README's
worked example, every script that relies on the exit code, and
`TestInstallMultiSkillRepoWithoutSelectionListsAndFails` all true. Interactivity
requires *both* stdin and stderr to be terminals — stderr because that is where
the block is drawn, and drawing escape sequences into a redirected stream is
worse than not asking.

**No flag.** TTY detection decides it. A `--no-input` escape hatch was
considered and rejected as surface that nothing yet needs: `--skill` and
`--all` already say what a script means, and they are what a script should say.

**Cancelling exits 1.** Backing out of the picker, and confirming with nothing
ticked, both return `prompt.ErrCancelled`, which reaches the root as an
ordinary error and exits `ExitError`. A cancelled install is not a successful
one, and a wrapper checking `$?` has to be able to see that.

**`--as` makes the picker single-select.** `--as` already means exactly one
skill — `runInstall` rejects it alongside `--all` or several `--skill`. Offering
checkboxes would let the user build a selection the flag cannot accept and only
find out afterwards, so the list takes one choice instead.

## Shape

The listing was already a typed error rather than a print — `channel.Ambiguous`
carries the candidates and leaves rendering to `cli` — so there is exactly one
place to hook into, and the plan, the executor, the receipts and the exit codes
are all untouched.

**`internal/prompt`** is a new package with one responsibility: choosing from a
list on a terminal. It never learns what a skill is; the caller passes labelled
rows and gets indices back. It is split so the repo's stdlib-only testing
reaches all of it — a pure model where a keystroke sequence is a fold
(`apply`, `render`, `decode`), and a thin driver (`Terminal`) that does raw
mode, reads bytes and redraws. The driver's redraw arithmetic is tested through
a small replay of the escape sequences it writes.

**The hook** sits in `resolveAmbiguity`, called only when `ch.Prepare` returns
`*channel.Ambiguous`. It declines to ask in two cases:

- the user passed `--skill` or `--all`. `narrow` also reports an ambiguity for a
  `--skill` that names nothing in the repository, and that is a typo rather than
  an unanswered question.
- there is no terminal.

**`channel.Ambiguous` gains `Resolved`**, the sha the listing describes. Having
chosen, `cli` re-reads with a *second, throwaway* request pinned to that sha.
Pinning matters twice: `gitx.Resolve` passes a full sha straight through, so the
second pass costs no network round trip, and a branch that moved between the
listing and the install can no longer install a tree the user was never shown.

The throwaway is the subtle part. `Git.Install` records `req.Ref` as the ref the
receipt tracks, so pinning the *real* request would write the sha there and
freeze the skill against every future `update` — an interactively-picked skill
would silently never update again. Only `lookup` carries the sha; `req` keeps
the ref the user asked for. `TestInstallPickedSkillTracksTheRefNotTheSha` is the
guard, and it checks the consequence as well as the field: a commit on top must
still read as `outdated`.

**Formatting stays in `cli`.** `rowLabels` builds the padded name-and-
description rows, and both the picker and the plain listing are built from it,
so a skill cannot look one way when it is offered and another when it is
reported. After the picker erases itself, `cli` prints the plain listing of what
was chosen, so the scrollback reads like the non-interactive form.

**`link <path>` inherits all of it**, since it shares `installOpts` and calls
`runInstall`, and the local channel shares `narrow`. A local directory of
several skills is picked from the same way; `Resolved` is empty there and the
re-read is another walk of the directory.

## Rejected

- **Carrying full candidates in `Ambiguous`** so no second `Prepare` is needed.
  It would hash every skill's tree for a listing that mostly gets thrown away,
  and it contradicts `brief()`'s stated purpose.
- **Re-preparing without the sha.** One line smaller, one `ls-remote` more, and
  it reopens the moved-branch race.
- **Treating a non-interactive multi-skill install as `--all`.** Convenient, and
  the one behaviour from which there is no recovering when it is wrong.
