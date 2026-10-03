# worktidy

Measured on Go 1.26.2, in a workspace whose sibling module was never published:
`go build .` succeeds, `go test ./...` succeeds, `go vet ./...` succeeds,
`go list -m all` succeeds and `go mod download` succeeds — and `go mod tidy`
**fails**, because it ignores `go.work` and goes to the network for the sibling:

```
go: finding module for package github.com/acme-unpublished-demo/shared
go: github.com/acme-unpublished-demo/api imports
	github.com/acme-unpublished-demo/shared: module github.com/acme-unpublished-demo/shared:
	git ls-remote -q --end-of-options https://github.com/acme-unpublished-demo/shared ...: exit status 128:
	remote: Repository not found.
```

`go work sync` does not help: it exits 0, and `go mod tidy` still fails afterwards.
`go mod vendor` is different — it refuses in workspace mode and names its own
replacement, so it has an official answer where tidy has none:

```
go: 'go mod vendor' cannot be run in workspace mode. Run 'go work vendor' to vendor the workspace or set 'GOWORK=off' to exit workspace mode.
```

This is [golang/go#50750](https://github.com/golang/go/issues/50750) — 161
reactions, 113 comments, closed **not planned** on 2025-05-10.

## Why the existing tool does not solve it

[`craigstjean/goworktidy`](https://github.com/craigstjean/goworktidy) adds the
`replace` directives and leaves them in `go.mod` permanently. That is the thing
the issue thread objects to, in its own words ([@fayep,
2025-03-23](https://github.com/golang/go/issues/50750#issuecomment-2746340340)):

> go.mod and go.sum are distributable artifacts. go.work is for local overriding
> of dependency modules for more streamlined development. Once development is
> done, I should not need to modify go.mod as I will have pushed my
> simultaneously developed dependency module as well as this one.

That repository has 0 stars, 0 forks and 0 tags, and its last commit is dated
2024-02-21.

`worktidy` runs the same edit as a transaction and removes it again: for each
module it snapshots `go.mod`, points the siblings at their directories on disk,
runs `go mod tidy` with `GOWORK=off`, then drops exactly the `replace`
directives it added.

## Setup

```sh
go install github.com/CalvinTjoaquinn/worktidy@latest
```

Then run it anywhere inside the workspace:

```sh
worktidy
```

## Output

From an actual run on a three-module workspace:

```
$ worktidy
  github.com/acme-unpublished-demo/api     go.mod updated
  github.com/acme-unpublished-demo/shared  unchanged
  github.com/acme-unpublished-demo/worker  go.mod updated

3 module(s) tidied, 0 replace directives left behind
```

Afterwards `api/go.mod` holds the `require` and no `replace`:

```
module github.com/acme-unpublished-demo/api

go 1.22

require github.com/acme-unpublished-demo/shared v0.0.0-00010101000000-000000000000
```

## What it changes in go.mod

- Adds the `require` lines `go mod tidy` decided on.
- Never leaves a `replace` behind. The directives it adds are temporary and are
  dropped by module path, one by one, so only the ones it added are removed.
- The `go` directive can rise, **because tidy raises it** — not because this tool
  touches it. Measured: a module at `go 1.22` that gains a real external
  dependency comes out at `go 1.26.0`, since `golang.org/x/mod v0.41.0` declares
  `go 1.26.0`.

## What it does not touch

Each of these has a test:

| guarantee | test |
|---|---|
| A `replace` you wrote is never claimed, never removed | `TestUserReplaceIsNeverTouched` |
| A sibling you already replaced is left exactly as it is | `TestExistingSiblingReplaceIsLeftAlone` |
| A module that needed nothing comes out byte-identical | `TestCleanModuleIsByteIdentical` |
| Running it twice changes nothing the second time | `TestIdempotent` |

Whatever `go.sum` tidy wrote is kept, as are tidy's other decisions.

Mutation goes through `go mod edit` as a subprocess rather than
`modfile.Format`, because `Format` canonicalises the whole file: rewriting an
unchanged `go.mod` can still change its bytes, which would break the
byte-identical guarantee above.

## Exit codes

| code | meaning |
|---|---|
| 0 | every module tidied, no `replace` left behind |
| 1 | setup problem: no `go.work` above the working directory, or a backup file from a previous run is still there |
| 2 | `go mod tidy` failed; that module's `go.mod` was restored byte-for-byte |
| 130 | interrupted |

On an interrupt, modules that already finished keep their changes and the one in
flight is rolled back. The signal handler only sets a flag — the single
goroutine that owns `go.mod` performs the rollback itself, so a restore can
never interleave with a `go mod edit`. The message claims a restore only when
one actually happened. Tests: `TestInterruptLeavesNoMess`, which asserts that no
backup and no `replace` survive wherever the signal lands, and
`TestInterruptedReportsHonestly`.

A backup file left on disk means a previous run died. `worktidy` refuses to start
rather than overwrite it, because that file is the only copy of the original
`go.mod` (`TestSnapshotRefusesWhenBackupExists`).

## Limitations

**Only `go mod tidy`.** Measured above: `go build`, `go test`, `go vet`,
`go list -m all` and `go mod download` are already workspace-aware, and
`go mod vendor` has `go work vendor`. Tidy is the gap.

**It stops at the first module that fails,** rather than continuing. A
half-tidied workspace is harder to reason about than an untidied one.

**For an unpublished sibling the `require` is a placeholder version,**
`v0.0.0-00010101000000-000000000000`. That is what Go writes for a module
resolved from disk, and it is unavoidable: a module that genuinely depends on an
unpublished module cannot have a resolvable `go.mod`. The consequence is that
such a `go.mod` is not resolvable outside the workspace. For a **published**
sibling — a monorepo whose modules are released but developed together — tidy
resolves a real version and this does not apply.

**You run `worktidy` instead of `go mod tidy`, every time** — not once to
repair things. For an unpublished sibling, running plain `go mod tidy` afterwards
still fails, now on the placeholder version rather than on a missing require:

```
go: example.com/app imports
	github.com/.../lib: github.com/.../lib@v0.0.0-00010101000000-000000000000: invalid version: ...
	remote: Repository not found.
```

**A module that mixes an unpublished sibling with a real external dependency
cannot be built even after tidying.** Measured: with the unpublished sibling
alone, `go build` succeeds after `worktidy`; add any real external dependency and
`go build` fails with `invalid version` on the placeholder, because Go then has
to resolve it. This is Go's behaviour, not this tool's — a hand-written `go.mod`
with identical contents, with `worktidy` never run, fails the same way. In that
situation the module needs a real `replace`, which is the one thing this tool
exists not to leave behind.

**If tidy raises a module's `go` directive above the one in `go.work`,** the next
workspace build fails until `go.work` is updated:

```
go: module . listed in go.work file requires go >= 1.26.0, but go.work lists go 1.22; to update it:
	go work use
```

**The Go team may fix `#50750` at any time, which would make this tool
irrelevant.** It is closed `not planned`, and `not planned` is not `never`.

## License

MIT. See [LICENSE](LICENSE).
