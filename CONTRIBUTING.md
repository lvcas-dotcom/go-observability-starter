# Contributing

Thanks for considering it. This project has one guiding constraint, and most
review feedback traces back to it:

> **Everything here has to be readable in one sitting.**

Someone lands on this repo because they want to understand how metrics, logs
and traces fit together — not to inherit another framework. A change that makes
the stack more capable but less readable is usually the wrong trade here, even
when it would be right in a production codebase.

## Getting set up

```bash
git clone https://github.com/lvcas-dotcom/go-observability-starter.git
cd go-observability-starter
make up      # build and start everything
make smoke   # assert every component actually works
```

You need Docker with the Compose v2 plugin, and Go 1.26+ for the app.

## Before you open a PR

```bash
make test      # go test -race
make lint      # go vet + golangci-lint
make validate  # compose, Prometheus, Alertmanager and Alloy configs
make smoke     # full end-to-end boot
```

CI runs all of these. `make smoke` is the one that matters most: it is what
lets the README claim the stack works after a single command.

## What tends to get merged

- Fixing something that is wrong, misleading, or has been deprecated upstream
- Instrumentation patterns that are genuinely worth copying into a real service
- Dashboard panels that answer a question the current ones cannot
- Documentation that removes a step someone would otherwise have to guess at

## What tends not to

- New backends that overlap with something already here
- Kubernetes manifests, Helm charts, Terraform — worth their own repo
- Framework abstractions over the standard library. The app uses `net/http`
  and `log/slog` on purpose: no dependency to learn before the observability
  parts make sense.
- Bumping every pinned version by hand. Dependabot handles that.

If you are unsure whether something fits, open an issue before writing it. That
is cheaper than a rejected PR for both of us.

## Conventions

**Go.** Standard library first. `gofmt` and `goimports` are enforced. Comments
explain *why*, not *what* — the code already says what it does. Anything
subtle enough to deserve a comment usually deserves a test too.

**Dashboards.** Every panel needs a unit and, where a number has a meaning,
thresholds. A p95 rendered as a bare `0.34` is not a finished panel. Attach a
screenshot to the PR.

**Alert rules.** Every alert needs a `for:` duration and an annotation that
says what to actually do about it. Alerts nobody can act on are how people
learn to ignore alerts.

**Configs.** If it can be validated by a tool, wire that tool into
`make validate` and CI.

**Docs.** User-facing changes go in both `README.md` and `README.pt-BR.md`. If
you are only comfortable writing one, do that and say so in the PR — someone
will pick up the other.

## Commits

Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, `ci:`) are preferred
but not enforced. A clear sentence beats a well-formed prefix.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
