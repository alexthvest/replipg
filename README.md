# Replipg

A single-instance Telegram group runtime in Go. Gameplay features register their
own behavior; the runtime provides update routing, durable work, and operations.

Install [devenv](https://devenv.sh/getting-started/), then use the repository's
pinned Go and lint tools. Every helper below enters `devenv shell` automatically.
No host Go toolchain or HTTP client dependency is required.

```sh
cp .env.example .env
chmod 600 .env
```

Edit the ignored `.env` locally: set `REPLIPG_BOT_TOKEN` to your test bot's
credential and `REPLIPG_STORAGE_DIR` to an absolute, clean directory path on a
persistent local filesystem. Create that directory before startup:

```sh
devenv shell
mkdir -p -- "$REPLIPG_STORAGE_DIR"
chmod 700 -- "$REPLIPG_STORAGE_DIR"
exit
./scripts/build
./scripts/run
```

The build writes `bin/replipg` (ignored). `scripts/run` compiles and runs the
command with the current configuration. The example deliberately leaves the
credential and storage path empty: running it unchanged exits unsuccessfully
and names the missing setting before database or network activity. Never put a
token in command arguments, shared files, logs, or screenshots. Keep local
environment files and machine paths out of shared devenv configuration.

Run exactly **one active replica** per deployment. Coordination between
instances and horizontal scaling are unsupported; `REPLIPG_DEPLOYMENT_MODE`
must remain `single`. Stop the old process before starting a replacement.
Update intake uses outbound long polling and needs no public inbound endpoint.
Startup removes conflicting webhook delivery while preserving pending updates.

`REPLIPG_STORAGE_FILESYSTEM=local` is an operator assertion, not automatic mount
detection. The directory must support local SQLite WAL semantics; do not use
NFS, SMB, or other network filesystems. Keep `replipg.db`, `replipg.db-wal`, and
`replipg.db-shm` together on that volume. Do not remove or copy individual
sidecars while the process is active. For a simple consistent backup, stop the
runtime and snapshot the entire storage directory before restarting it.
Compatible migrations run during startup before readiness. Migration failure
blocks startup; an older binary refuses a database with a newer schema.
Rollback requires a compatible binary/database snapshot pair or a forward fix.

Health endpoints are unauthenticated. Keep the example loopback bind, or use a
private interface protected by infrastructure access control. In another terminal:

```sh
./scripts/probe livez
./scripts/probe readyz
./scripts/probe statusz
```

The probe defaults to `readyz` and prints only the endpoint and HTTP status,
with a nonzero exit on failure. It uses `REPLIPG_HEALTH_ADDRESS` from `.env`.
`/livez` indicates process liveness; `/readyz` returns 200 only after storage,
migrations, recovery, and polling initialization succeed, and 503 during unsafe
states. `/statusz` exposes safe aggregate durable-work counts and deployment
limitations. Probes and diagnostics exclude tokens, message bodies, private
response content, and raw Telegram request/response bodies. Send SIGINT
(Ctrl-C) or SIGTERM to stop: readiness drops, new work stops, running work drains
up to `REPLIPG_SHUTDOWN_DEADLINE`, then remaining work is cancelled and preserved
for recovery on restart.

To receive ordinary messages in a Telegram test group, disable the bot's Privacy
Mode through BotFather (and re-add the bot if Telegram requires it), or grant
equivalent group access. Confirm ordinary messages reach the bot; receiving
commands alone does not establish this. Public and user-specific ephemeral
responses use their registered transport capability. Unsupported or failed
ephemeral delivery has no private-chat or public-message fallback. This setup
does not itself register gameplay or demonstration commands.

Run the automated local quality gates without Telegram credentials:

```sh
./scripts/check
```

This checks formatting and runs `go test ./...`, `go vet ./...`,
`golangci-lint run`, and `go test -race ./...` inside devenv. Tests use fake
Telegram boundaries and must not contact real Telegram. All required runtime
environment names and non-secret timeout examples are listed in `.env.example`;
the claim lease must be at least the handler timeout, and maximum retry delay
must be at least the initial delay.

For an opt-in development transport smoke check, use a dedicated test bot and
an isolated test group. Set `REPLIPG_DEV_SMOKE=true` in the ignored `.env`, then
run `./scripts/run`. The flag is optional and defaults to false; normal startup
registers no demonstration behavior. Enabling it installs these probes:

- Send `/dev_smoke` in the test group: the bot sends a fixed public response
  with a **Restricted reply** button.
- Press the button: Telegram receives an invisible callback acknowledgement,
  followed by a response restricted to the user who pressed it in that group.
- Send `/dev_smoke_ephemeral` as an incoming ephemeral group message when the
  client supports it: the bot replies using that ephemeral message's identity.
  An ordinary command with this name lacks the required authorization and
  produces no message.
- Send an ordinary group message: the bot sends a fixed receipt without echoing
  its content or assigning gameplay outcomes. Privacy Mode must allow intake.

Unsupported or rejected ephemeral delivery records a safe failure and sends no
public or private-chat fallback. The callback and incoming ephemeral probes use
15-second authorization windows. Diagnostics never print the probe content,
incoming message bodies, or raw API responses. Stop the bot and unset the flag
(or set it to `false`) after testing. The deterministic fake-Telegram transport
check requires no bot credentials and sends no real Telegram messages:

```sh
devenv shell -- go test ./internal/devsmoke ./internal/config ./cmd/replipg
```

Run the completed runtime smoke gate without Telegram credentials:

```sh
./scripts/smoke
```

This builds and boots the production executable with an isolated temporary
SQLite directory and the development probes enabled. A test-owned HTTPS proxy
routes only `api.telegram.org:443` to a local TLS fake; no real Telegram
connection is made. Proxy and certificate trust settings apply only to the
child process. The gate checks live/not-ready startup, ready/status probes with
both durable workers active, public and restricted replies, invisible callback
acknowledgement, ordinary-message intake, rejected restricted delivery without
fallback, SIGTERM shutdown, closed health listener, and the durable cursor after
storage reopens. It prints only test results and safe failure descriptions.
