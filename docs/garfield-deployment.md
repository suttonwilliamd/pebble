# Garfield remote dogfood service

## Status and safety boundary

The v1 test server is `cmd/pebbled`; the container definition is
`deploy/docker/compose.yaml`. It serves one isolated repository and a small landing
page. It does **not** use the legacy `internal/remote` prototype.

Garfield deployment is **not yet performed**: SSH reaches Garfield from the
workstation, but authentication is denied. No Garfield services, firewall rules,
Cloudflare configuration or DNS records have been changed. Obtain an approved
SSH user/key before deployment. Never paste private keys, passwords or tokens
into chat or Git.

Start with **loopback + SSH forwarding**, using disposable data. A Cloudflare
hostname is optional after the remote acceptance tests pass and the owner approves
that specific hostname/exposure. Leave the apex website, dashboard, torrents,
Hermes, and every existing stack/tunnel route unchanged.

## Protocol and limits

- `/` is a static landing page; `/healthz` returns `ok`. Neither exposes repository data.
- `/v1/refs`, `/v1/push`, `/v1/fetch` require a strong shared bearer token.
- Every transfer includes the complete typed commit/tree/blob history for one
  branch, SHA-256 checked before ref publication. Unreachable extras are rejected.
- Push is single-ref compare-and-swap and fast-forward only. No force push or ref deletion.
- Fetch imports into `incoming/<branch>` without changing HEAD or working files.
  A checked-out incoming branch cannot be fetched into; create/checkout a work branch first.
- One token grants read/write access to the entire test repository; no accounts/RBAC.
- Maximum 32 MiB JSON per request/response and 50,000 objects; whole history is sent
  again on each sync. No incremental negotiation, resumability, merge/pull or ROCK integration.
- Clients require HTTPS, except for loopback HTTP. Redirects are refused to avoid token forwarding.
- One server instance per data volume. Objects precede atomic ref publication;
  directory fsync/power-loss durability and automatic stale-lock recovery are not implemented.
- The container is non-root, read-only except its dedicated data volume, drops all
  capabilities and has CPU/memory/PID/log limits. Only host loopback port 7931 is published.
- This is a trusted-operator lab, not a hostile multi-tenant service. Authenticated
  clients can consume disk space; Docker does not impose a volume disk quota here.

## First deployment on Garfield (operator commands)

First inspect existing containers, listeners, disk space and backup capacity.
Confirm `/opt/stacks/pebble` is unused and port 7931 is free. Do not reuse an
existing service's data directory or credentials.

```sh
docker compose version
docker ps --format 'table {{.Names}}\t{{.Ports}}'
ss -ltn | grep ':7931 '  # must have no existing listener
df -h /var/lib/docker /opt

# Run only after verifying this is a NEW stack directory.
sudo install -d -m 0755 -o "$(id -u)" -g "$(id -g)" /opt/stacks/pebble
git clone https://github.com/suttonwilliamd/pebble.git /opt/stacks/pebble/source
sudo install -d -m 0700 /opt/stacks/pebble/secrets
sudo sh -c 'umask 077; openssl rand -hex 32 > /opt/stacks/pebble/secrets/sync-token'
sudo chown 65532:65532 /opt/stacks/pebble/secrets/sync-token
sudo chmod 0400 /opt/stacks/pebble/secrets/sync-token

compose() {
  sudo env PEBBLE_TOKEN_FILE=/opt/stacks/pebble/secrets/sync-token \
    docker compose -f /opt/stacks/pebble/source/deploy/docker/compose.yaml "$@"
}
compose config --quiet
compose up -d --build
compose ps
curl --fail http://127.0.0.1:7931/healthz
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7931/v1/refs # expect 401
```

The secret's UID 65532 matches the container process. Run Compose through sudo so
its file-backed secret can be inspected/mounted without weakening host file permissions.
Do not print the token. To give the operator an SSH-transferable client copy:

```sh
install -d -m 0700 "$HOME/.config/pebble"
sudo install -m 0600 -o "$(id -u)" -g "$(id -g)" \
  /opt/stacks/pebble/secrets/sync-token "$HOME/.config/pebble/garfield.token"
```

Transfer that client file with SCP over the verified SSH connection into a private
local directory **outside** source repositories. Back it up as a secret, not as code.
Rotate by replacing both copies, preserving ownership/mode, and recreating the container.

## Remote acceptance test

From the workstation, forward the loopback listener:

```sh
ssh -N -L 17931:127.0.0.1:7931 suttonwilliamd@Garfield
```

Use the actual approved SSH account/key; `suttonwilliamd` is the account in the
existing Garfield runbook, not a claim that this workstation can authenticate.
Set `PEBBLE_TOKEN_FILE` to the private client-token path, plus `PEBBLE_AUTHOR` and
`PEBBLE_EMAIL`. Build the CLI with `go build -o bin/pebble ./cmd/pebble` (`.exe` on Windows).

In disposable **client A**:

```sh
pebble init
# create note.txt and a small binary file
pebble commit 'first snapshot'
# edit note.txt
pebble commit 'second snapshot'
pebble push http://127.0.0.1:17931
```

In a separate disposable **client B**:

```sh
pebble init
pebble fetch http://127.0.0.1:17931
pebble checkout incoming/main
pebble log                    # both commits must be present
pebble fsck
pebble branch work
pebble checkout work
# edit note.txt
pebble commit 'client B edit'
pebble push http://127.0.0.1:17931 main
```

Compare binary file hashes on both clients. Client A's old push must now fail
(HTTP 409); it must not overwrite B's commit. Fetch again from A, inspect
`incoming/main`, and verify all three commits. Invalid/missing tokens must fail
with 401, and malformed/oversized bundles must not publish refs. Restart only the
Pebble container and repeat fetch into a fresh client to prove volume persistence.
CI performs a Compose two-client roundtrip, stale-push rejection and restart test;
Go tests additionally cover binary preservation, concurrent CAS, tampering, unsafe
paths, protocol versions, auth, request limits and redirects.

`init` creates `.pebbleignore`; checkout adopts byte/mode-identical untracked files
without rewriting them, allowing this initial file to coexist with the fetched
snapshot. Different contents still cause a collision/refusal.

## Optional Cloudflare ingress — not applied

Suggested hostname: `pebble.william64.com`, **subject to owner approval and checking
that it is unused**. If cloudflared is a host service, its origin can be
`http://127.0.0.1:7931`. If cloudflared runs in Docker, its `localhost` is not the
host: inspect the topology and approve a dedicated network/proxy arrangement;
do not fix this by binding the Pebble port to every interface.

Use HTTPS at the edge. Retain the application bearer token. Do not log Authorization
headers. Consider rate limits and IP restrictions before any public test. A browser-only
Cloudflare Access policy will block the CLI; Access service-token header support is
not implemented yet. Keep using SSH until the chosen policy is tested end to end.
Any Cloudflare API credential should be narrowly scoped and supplied through a secret
file/environment outside Git, never in command-line arguments or chat.

## Backups, rollback and verification record

Stop writes before copying the dedicated named volume, and keep an independent
backup outside that volume. Token recovery is separate. Test restoration into a
**different volume and loopback port**, never over the live volume. A host-side
backup/restore drill is still required before trusting this service with valuable work.

`compose stop` disables only this stack. `compose down` removes its container and
network but preserves data. **Never use `down -v` on the persistent Garfield stack.**
Removing a future ingress route must affect only the approved Pebble hostname.

After live deployment, record the Git commit/image ID, volume name, port, health
results, two-client checks, restart result and backup/restore evidence in Garfield's
private operations repository, without tokens or raw credential-bearing logs.
