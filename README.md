# push

A small status-push service for known clients.

- **Clients** `POST` their latest JSON status to a public HTTP port (port 80
  behind your reverse proxy, which terminates TLS).
- **Consumers** `GET` the last status of any client from a separate local
  HTTP service (default port 8000, no proxy in front).
- Each client has a **name** (letters/numbers/dashes) and a **token**. The
  config stores only the tokens' **SHA-256 hashes**.
- Only the **last** status per client is kept, in memory, together with the
  time it was received and the client's **true IP address**.
- One YAML **config file** (clients, consumers, limits, bind addresses),
  loaded at startup and reloaded on **SIGHUP**.
- Single static **Go** binary; installed on Debian VMs/LXCs via **ansible**.

## How it works

```
                 reverse proxy (TLS)
                     ┌──────────┐
clients ──HTTP:443──▶│  nginx   │──HTTP──▶ push :80     (clientapi)
                     └──────────┘           │
                                            ▼ in-memory store
consumers ──HTTP:8000────────────────▶ push :8000    (consumerapi)
         (local interface / firewalled, no proxy)
```

### Client API (port 80)

`POST /{client-name}`

| item | value |
|---|---|
| Header | `X-Client-Token: <raw token>` |
| Body | any valid JSON, at most `max_status_bytes` (default 100 KiB); chunked transfer is fine |

For a well-formed request (exact `/{name}` path with a valid name) the
response never reveals whether the name is registered or the token is
valid: an oversized body gets `413` and everything else gets `200`,
regardless of name or token. The status is stored only if the client name
is known, the token matches, and the body is valid JSON within the size
limit; otherwise the request is silently dropped. The stored status
carries the reception time (UTC) and the client's true IP:

- If the request's **source address is a configured `forwarder_ips` entry**
  (IP or CIDR): the **rightmost valid IP in `X-Forwarded-For` that is not
  itself a configured forwarder** is used. This is correct whether the proxy
  appends to the header (nginx default) or replaces it, and a client cannot
  win by forging entries on the left. If the header is missing or has no
  such IP, the forwarder's source address is used.
- Otherwise the source address is used and `X-Forwarded-For` is ignored.

Responses: `200` ok or silently dropped · `400` unreadable body · `413`
body over limit · `404` malformed path (not a possible client name) ·
`405` wrong method · `429` rate limit. The rate limit applies to *every*
request from a source IP — including wrong-method and malformed-path
requests — so no request type can bypass it.

```sh
curl -X POST https://push.example.com/sensor-1 \
     -H 'X-Client-Token: <token>' \
     -d '{"temp":21.5,"hum":40}'
# -> {"ok":true}
```

### Consumer API (port 8000, local)

All endpoints except health require `Authorization: Bearer <raw token>`
matching a configured consumer. Every response carries
`Cache-Control: no-store`, and every request is rate-limited per source IP
(500 requests/second, before authentication), so a hostile internal host
can neither flood the service nor hammer the auth endpoint.

| endpoint | auth | response |
|---|---|---|
| `GET /` | no | `200` if the service is up (health) |
| `GET /status` | yes | all client names + last status times |
| `GET /status/{name}` | yes | last status of one client |

`GET /status/{name}` returns:

```json
{
  "name": "sensor-1",
  "time": "2026-07-18T00:40:12.123456789Z",
  "source_ip": "203.0.113.77",
  "status": {"temp": 21.5, "hum": 40}
}
```

`404` is returned for unknown clients, and (with a distinct body message)
for known clients that have not sent a status yet.

```sh
curl -H 'Authorization: Bearer <token>' http://127.0.0.1:8000/status/sensor-1
curl -H 'Authorization: Bearer <token>' http://127.0.0.1:8000/status
curl http://127.0.0.1:8000/            # health
```

## Reference client

For any POSIX-ish system with `cron`, `bash` and `curl`: one small script,
[client/push.sh](client/push.sh), and one config file.

Write the endpoint, the client `name` and its `token` to a file, e.g.
`/etc/push-client.conf`:

```
url=https://push.example.com
name=sensor-1
token=...
```

...and add a cron line. An optional JSON file is sent as the status;
without it the script sends `{}`:

```
* * * * * /usr/local/bin/push.sh /etc/push-client.conf /path/to/status.json
```

The script prints nothing on success; failures are left for cron to mail.

## Configuration

See [config.example.yaml](config.example.yaml) for a commented example.

| key | required | default | meaning |
|---|---|---|---|
| `client_listen` | yes | – | bind address, `host:port`, public client service |
| `consumer_listen` | yes | – | bind address, `host:port`, local consumer service |
| `forwarder_ips` | no | empty | trusted proxy IPs/CIDRs; only their requests honor `X-Forwarded-For` |
| `max_status_bytes` | no | `102400` | max status payload in bytes |
| `rate_limit` | no | enabled: `60` requests / `1m` per IP | per-IP fixed-window limit on the client service, applied to every request (`enabled: false` disables it) |
| `clients` | no | empty | list of `{name, token_sha256}` |
| `consumers` | no | empty | list of `{name, token_sha256}` |

Unknown YAML keys, multiple YAML documents, and whole-address-space
forwarders (`0.0.0.0/0`, `::/0`) are rejected. `token_sha256` is the
lowercase 64-hex-char SHA-256 of the raw token.

### Managing tokens

```sh
push gen-token
# token:        3f0626cf...
# token_sha256: 9d6c...          <- only this goes into the config
```

Give the raw token to the client/consumer over a secure channel. The config
never contains raw tokens.

### Reload

```sh
systemctl reload push     # sends SIGHUP
```

The new file is validated first; if invalid, the previous configuration
keeps running (the failure is logged). Bind-address changes require a
restart (a warning is logged on reload).

## Building and testing

Requires Go ≥ 1.23. The only dependency is `gopkg.in/yaml.v3`.

```sh
make build        # static binary at build/push
make test         # go test -race ./...
make lint         # golangci-lint (installs v2.15.0 to GOPATH/bin if missing)

# The example config only passes validation once its placeholder token
# hashes are replaced (push gen-token):
./build/push validate --config config.example.yaml
./build/push run --config config.example.yaml
```

Linting uses [`golangci-lint`](https://golangci-lint.run/) v2, configured in
`.golangci.yml` (gosec, bodyclose, noctx, staticcheck, gofumpt, …). `make lint`
installs it automatically if it is not already on your `PATH` or in
`GOPATH/bin`.

## Installing with ansible (Debian VM or LXC)

The ansible role ships a prebuilt static binary (no Go toolchain needed on
the target), creates a `push` system user, installs an nftables ruleset,
the config and a systemd unit, and enables the service.

```sh
make build                                     # 1. build the binary
cp ansible/inventory.example.ini inventory.ini # 2. point it at your host(s)
# 3. set real values in ansible/playbook.yml vars:
#      push_client_listen / push_consumer_listen
#      push_eth_iface (the host's primary interface, for the firewall)
#      push_forwarder_ips  (your proxy's source IP as seen by the host)
#      push_client_allow_sources (who may reach port 80; default: the forwarders)
#      push_clients / push_consumers (push gen-token output)
#      push_consumer_allow_sources (who may reach port 8000; one /32 per consumer host)
ansible-playbook -i inventory.ini ansible/playbook.yml   # 4. install
```

Operational notes:

- Runs non-root with `AmbientCapabilities=CAP_NET_BIND_SERVICE` (needed for
  port 80). If you bind only ports ≥ 1024, the capability is harmless.
- The role installs an nftables ruleset (policy drop: loopback, ssh, the
  client port only for `push_client_allow_sources` — default: the forwarder
  IPs, so clients cannot skip the proxy and its TLS — and the consumer port
  only for `push_consumer_allow_sources`) and applies it **before** the
  service is started, so neither service port is ever open without its
  rules. Set `push_eth_iface` if the primary interface is not `eth0`. If
  the host previously ran ufw, disable it first: the ruleset replaces it.
- The config file is `0640 root:push`. The consumer port has no proxy in
  front of it: bind it to an internal interface (e.g. `10.0.0.5:8000`).
  With no `push_consumer_allow_sources` entries it is unreachable from any
  source.
- Log output goes to the journal: `journalctl -u push -f`.

## Security notes

- Only token hashes are stored in the config; verification is
  SHA-256 + constant-time compare.
- Client names and tokens cannot be probed: for a well-formed path the
  response depends only on the body (oversized → `413`, otherwise `200`),
  never on the name or token, and unknown names are checked against a dummy
  hash so verification timing is uniform as well.
- `X-Forwarded-For` is honored **only** for requests sourced from
  `forwarder_ips` (rightmost-entry rule, see Client API); everyone else is
  recorded by source address, so spoofed headers from the open internet
  cannot fake a client's IP. Keep the forwarder list narrow (the proxy's
  own IPs): the rule assumes the proxy's appended entry — the client's
  true address — is not itself a forwarder.
- The rate limit on the client port is applied per resolved client IP
  before anything else, to every request regardless of method or path
  (on by default: 60 requests/minute per IP; brute-force and DoS
  protection).
- The consumer port rate-limits every request per source IP (500
  requests/second, before authentication) and answers with
  `Cache-Control: no-store`; combined with the firewall allow-list this
  caps what a compromised internal host can do there (token brute force
  remains infeasible: 256-bit tokens, constant-time compare).
- Statuses live in memory only; a restart loses them (a later version may
  forward to a datastore).
- Anyone who can read a consumer token can read all client statuses.
  Consumer tokens are not scoped per client (by design, for now).

## Project layout

```
main.go                     CLI: run / gen-token / validate
internal/app                wiring, Run, SIGHUP reload
internal/config             YAML config load + validation
internal/clientapi          public client service (port 80)
internal/consumerapi        local consumer service (port 8000)
internal/store              in-memory last-status store
internal/ratelimit          fixed-window per-key limiter
internal/xff                client IP resolution from X-Forwarded-For
internal/token              sha256 hash/compare/generate
internal/httpjson           JSON response helpers
client/                     bash reference client (cron + curl)
ansible/                    playbook + push role (Debian)
```
