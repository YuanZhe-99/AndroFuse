# AGENTS.md

Operating guide for agents working on **AndroFuse**. This file holds **only** rules about how to
work here. Everything describing what the code *is* or *does* lives in `doc/en-us/` — see
[Where to read what](#where-to-read-what).

AndroFuse is an open-source, modern Windows desktop integration client for Samsung DeX devices and
general Android devices (plus an optional same-repo Android Companion APK in later milestones). It
uses **MyGo** native UI (`package ui`), **Go** orchestration (`cmd/androfuse`, `cmd/androfuse-svc`),
and a **Rust** core library (`core/androfuse-core`) shared across Windows (`x64` and native `ARM64`)
and Android (`arm64-v8a`). Treat the user's message as the change request: plan, implement, verify,
report.

## Reading order

When you need to understand code, read in this order and stop as soon as you have what you need:

1. **`doc/en-us/`** — start here, always. `architecture.md` for shape and rules;
   `functions/INDEX.md` to find the right package or crate; the concept docs for behavior.
2. **`PLAN.md`** — while it is active, for milestones, compatibility matrices, invariants (`INV-xx`),
   and unimplemented designs.
3. **Comments in the source** — the Function Explanation Layer on each declaration.
4. **The implementation** — only when the docs and comments are insufficient, or when you must
   confirm actual behavior before changing it.

Do not jump straight to reading source bodies. Where docs and code disagree on something you are
about to change, verify against the code, then fix the docs in the same commit.

## Where to read what

| Question | Read |
|---|---|
| Active plan, requirements fusion, compatibility matrix, milestones (`M0–M5`) | `PLAN.md` |
| Processes, packages, crates, C ABI (`androfuse_abi_version`), state | `doc/en-us/architecture.md` |
| Wire protocols (`scrcpy v5.0.1`, DeX loopback, Miracast, NV12 ABI v2, leases) | `doc/en-us/protocols.md` |
| Verified upstream contracts (MyGo, scrcpy, DeXDesk, ScrcpyDeX, DX-Manager, MyDeX, DeXter, Kyokai) | `doc/en-us/upstream.md` |
| Reused third-party code, licenses, and trademark notices | `doc/en-us/third-party.md` |
| What is implemented and verified per Windows architecture and Android/One UI version | `doc/en-us/platform-notes.md` |
| Environment, commands, dev loop, icon generation | `doc/en-us/development.md` |
| English→Chinese terminology | `doc/en-us/translation-guide.md` |
| Past releases and checked upstream versions | `doc/en-us/version-history.md` |
| Per-package and per-crate roles | `doc/en-us/functions/INDEX.md` |

New concept docs are added with the code they describe; add their rows here in the same commit.

## Required workflow

1. Treat the user's message as the modification request.
2. Before editing, fetch every configured remote and check whether the local branch is behind.
   Resolve any divergence before starting.
3. Read per [Reading order](#reading-order). Multi-step plans live in `PLAN.md`.
4. Keep changes scoped. Do not create empty placeholder crates or stub packages for future milestones.
5. Update documentation in the same change set — see
   [Documentation maintenance](#documentation-maintenance).
6. Verify — see [Verification](#verification).
7. Report briefly, in English and Chinese: what changed, what was verified (with evidence), the
   current version, the configured remotes, and anything that could not be done.
8. Ask before pushing. The user decides when to release.

## Documentation maintenance

**Docs are the primary artifact. Update them first, and never let them drift.**

Any change to a behavior, a data format, the FFI, the local API, a work mode, or a user-visible
feature updates, in the same commit, the affected concept docs and the `functions/INDEX.md` row.

Every language directory under `doc/` (`en-us` and `zh-cn`) mirrors the others exactly — same
files, headings, tables, and code blocks. `doc/en-us/` is authoritative: any documentation change
updates **all** language directories in the same commit, translated per `translation-guide.md`; new
terms go into its glossary. `go run ./tools/checkdocs` must pass.

**Put explanation in the docs, not here.** This file is for agent instructions only.

**Plans live only in `PLAN.md`** — never in `doc/`. Delete items from `PLAN.md` as soon as they
move into implemented concept docs; delete `PLAN.md` when the plan is complete.

Add a `doc/en-us/version-history.md` entry for each release. Documentation-only commits do not
bump versions or create tags.

## Authoring rules

**Function Explanation Layer.** Every function, method, and significant type carries a doc comment
(Go doc comments; Rust `///`) whose first sentence states its purpose ("RunSelfCheck probes …").
Non-trivial declarations add short lines as needed:

- `Inputs:` important parameters only
- `Returns:` what the caller receives
- `Side effects:` state, file, network, process, or UI effects
- `Notes:` assumptions, edge cases, invariants (reference `INV-xx` where enforced)

Keep comments accurate in the same change as the code. Every Rust `unsafe` block carries a
`// SAFETY:` comment (`clippy::undocumented_unsafe_blocks` is denied), and every FFI export states
who owns each pointer it takes or returns.

**Code guidelines** (shared by the MyApps projects):

- Do not over-scope: make only requested or clearly necessary changes; no speculative flags or empty
  modules waiting for later work.
- Do not over-abstract: no single-implementation traits/interfaces, no builders for simple structs;
  fewer cohesive files beat thin wrappers.
- Do not over-defend: validate at true boundaries (FFI entry points, ADB/RTSP/socket inputs, disk
  files, OS APIs) and trust internal callers.
- Use the pinned language versions idiomatically (Go 1.27, Rust 2024 edition).
- Name concisely; reuse existing helpers; nothing allocates per frame on video/input paths.

Other conventions:

- MyGo UI code follows `.agents/skills/mygo-maintenance/SKILL.md`: views are functions of state,
  elements are never stored, background results reach views via `Window.Update`, and controls get
  stable `c.Key(...)` identities before children.
- Interface copy is written for people using the app (no raw internal error codes or paths in main
  views; diagnostics belong on the Diagnostics view).
- Interface strings go through the ARB catalogs (`locales/en.json`, `locales/zh-Hans.json`);
  `go tool mygo i18n check` must pass, and `go tool mygo generate` commits `locales/catalog_gen.go`.
- Layout numbers and colors are named tokens in `internal/gui/style.go`, not scattered literals.
- Secret-shaped test fixtures (tokens, device keys, hashes) begin with `0123456789abcdef` or
  contain `fixture`, `dummy` or `fake`, so `.gitleaks.toml` covers them; never add per-commit
  fingerprints to `.gitleaksignore` for test fixtures.

## Behavior contract

Do not change these without the user explicitly deciding to:

- **Three modes are never artificially mutually exclusive (`INV-01`).** Samsung DeX, Android Phone
  Mirroring, and Independent App Windows coexist when device hardware/encoders allow; incompatible
  combinations trigger capability detection, clear user notice, and safe fallback.
- **Native GPU video path (`INV-04`).** Video decoding uses Windows native Media Foundation / DXVA
  directly into `ui.Bitmap.UpdateYUV` (D3D11) without JS/webview frame forwarding or per-frame
  allocations.
- **Honest telemetry (`INV-05`).** Target resolution/FPS settings are never presented as guarantees;
  always show actual negotiated resolution and measured FPS.
- **Real DeX vs fallback (`INV-06`).** Never misrepresent a plain AOSP virtual display as full
  Samsung DeX; validate DeX multi-window, taskbar, Recents, and pointer behavior and label the
  active backend.
- **Secure defaults (`INV-07`, `INV-08`, `INV-09`).** Encrypted TLS wireless ADB pairing is
  preferred; plaintext `adb tcpip` is disabled by default and requires explicit confirmation; remote
  access and MCP control are off by default; secrets and device identifiers are scrubbed in logs;
  never require Root or bypass Android security/`FLAG_SECURE`.
- **Native ARM64 parity (`INV-10`).** Windows 11 `x64` and native `ARM64` are equal first-release
  targets; never claim ARM64 support via x64 emulation.
- **No fake implementations.** Never claim an unverified feature, device, or architecture as
  supported.

## Verification

```bash
gofmt -l cmd internal tools
go vet ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet .
go tool mygo i18n check
go run ./tools/checkdocs
cargo fmt --manifest-path core/Cargo.toml --all --check
cargo clippy --manifest-path core/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path core/Cargo.toml --workspace
cargo deny --manifest-path core/Cargo.toml check licenses bans sources
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --no-banner --redact -v .
```

- Run the `ui.NewTester` render checks (`internal/gui`) across `en` and `zh-Hans`, light and dark,
  and desktop/mobile viewports whenever UI changes.
- **Never claim "verified" without an actual run;** record verified hardware, OS, and architecture
  evidence in `doc/en-us/platform-notes.md`.

## Release, version, commit, tag, push

The version lives in `internal/version/version.go` (`Version`); `mygo.json` must match (enforced by
`internal/version/version_test.go`). Do not bump versions or create tags until the user confirms.

## Agent co-author attribution

An agent that made a real, material contribution to a commit may add its own accurate
`Co-authored-by:` trailer using its documented identity; never copy trailers automatically or list
non-contributing models.

## Remotes and secrets

- `origin` → `<local_gitea_address>` (private Gitea)

**Masking rule:** keep `origin` written as `<local_gitea_address>` in every committed file. Never
write the underlying host, Tailscale domain, port, or machine-specific absolute paths anywhere in
the repository.

**Never commit:** secrets, signing keys or certificates, `.dev/`, `build/`, `.mygo/`, `core/target/`,
built binaries under `resources/<goos>-<goarch>/`, or machine-specific state.

License: `GPL-3.0-only` (`LICENSE`). Keep third-party notices in `THIRD_PARTY_NOTICES.md` and
`doc/en-us/third-party.md`. Never copy proprietary code or assets from commercial apps (e.g.
AndroMeld).
