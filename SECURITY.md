# Security Policy

## This is a local demo stack, not a deployable one

The defaults in this repository trade security for a frictionless first boot.
That is a deliberate choice for something you run on `localhost`, and a bad one
anywhere else. Before this goes near a shared network, at minimum:

| Default | Why it is fine locally | What to do otherwise |
|---|---|---|
| Grafana anonymous access as `Admin`, login form disabled | No credentials to look up before you see a dashboard | Remove `GF_AUTH_ANONYMOUS_*`, set a real admin password, put it behind SSO |
| `auth_enabled: false` in Loki, no auth in Tempo, Prometheus or Alertmanager | Everything is bound to your machine | Front them with a reverse proxy that authenticates, or keep them off any routable interface |
| `/var/run/docker.sock` mounted into Alloy | It is how container logs are discovered | Ship logs from the app directly, or use a log driver — socket access is effectively host root, and read-only does not change that |
| All ports published to the host | You want to click them | Publish nothing, or bind to `127.0.0.1` explicitly |
| No TLS anywhere | Loopback traffic | Terminate TLS at a proxy |
| No retention limits worth the name | A laptop demo | Size retention and ingestion limits to your actual disk |

The single most important one is the Docker socket. A container that can reach
`docker.sock` can start a privileged container and own the host. It is mounted
here because that is how log discovery works in a compose-only setup, and it is
called out rather than hidden.

## Reporting a vulnerability

If you find a security issue in the code in this repository — as opposed to one
of the documented trade-offs above — please report it privately through
[GitHub Security Advisories](https://github.com/lvcas-dotcom/go-observability-starter/security/advisories/new)
rather than opening a public issue.

Expect an initial response within a week.

## Supported versions

Only `main` is maintained. There are no backported fixes.
