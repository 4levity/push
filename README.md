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
| Body | any valid JSON, at most `max_status_bytes` (default 100 KiB) |

The request is accepted (HTTP 200) only if the client name is known, the
token matches, and the body is valid JSON within the size limit. The status
is stored with the reception time (UTC) and the client's true IP:

- If the request's **source address is a configured `forwarder_ips` entry**
  (IP or CIDR): the **rightmost valid IP in `X-Forwarded-For` that is not
  itself a configured forwarder** is used. This is correct whether the proxy
  appends to the header (nginx default) or replaces it, and a client cannot
  win by forging entries on the left. If the header is missing or has no
  such IP, the forwarder's source address is used.
- Otherwise the source address is used and `X-Forwarded-For` is ignored.

Responses: `200` ok · `400` invalid JSON · `401` bad/missing token ·
`404` unknown client or bad path · `405` wrong method · `413` body over
limit · `429` rate limit.

```sh
curl -X POST https://push.example.com/sensor-1 \
     -H 'X-Client-Token: <token>' \
     -d '{"temp":21.5,"hum":40}'
# -> {"ok":true}
```

### Consumer API (port 8000, local)

All endpoints except health require `Authorization: Bearer <raw token>`
matching a configured consumer.

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

## Configuration

See [config.example.yaml](config.example.yaml) for a commented example.

| key | required | default | meaning |
|---|---|---|---|
| `client_listen` | yes | – | bind address, `host:port`, public client service |
| `consumer_listen` | yes | – | bind address, `host:port`, local consumer service |
| `forwarder_ips` | no | empty | trusted proxy IPs/CIDRs; only their requests honor `X-Forwarded-For` |
| `max_status_bytes` | no | `102400` | max status payload in bytes |
| `rate_limit.enabled` | no | `false` | enable per-IP fixed-window limit on the client service |
| `rate_limit.max_requests` | no | – | max requests per client IP per window |
| `rate_limit.window` | no | – | window, Go duration (`30s`, `5m`, `1h`) |
| `clients` | no | empty | list of `{name, token_sha256}` |
| `consumers` | no | empty | list of `{name, token_sha256}` |

Unknown YAML keys are rejected. `token_sha256` is the lowercase 64-hex-char
SHA-256 of the raw token.

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
./build/push validate --config config.example.yaml
./build/push run --config config.example.yaml
```

Linting uses [`golangci-lint`](https://golangci-lint.run/) v2, configured in
`.golangci.yml` (gosec, bodyclose, noctx, staticcheck, gofumpt, …). `make lint`
installs it automatically if it is not already on your `PATH` or in
`GOPATH/bin`.

## Installing with ansible (Debian VM or LXC)

The ansible role ships a prebuilt static binary (no Go toolchain needed on
the target), creates a `push` system user, installs the config and a
systemd unit, and enables the service.

```sh
make build                                     # 1. build the binary
cp ansible/inventory.example.ini inventory.ini # 2. point it at your host(s)
# 3. set real values in ansible/playbook.yml vars:
#      push_client_listen / push_consumer_listen
#      push_forwarder_ips  (your proxy's source IP as seen by the host)
#      push_clients / push_consumers (push gen-token output)
#      push_consumer_allow_sources (optional ufw sources for port 8000)
ansible-playbook -i inventory.ini ansible/playbook.yml   # 4. install
```

Operational notes:

- Runs non-root with `AmbientCapabilities=CAP_NET_BIND_SERVICE` (needed for
  port 80). If you bind only ports ≥ 1024, the capability is harmless.
- The config file is `0640 root:push`. The consumer port has no proxy in
  front of it: bind it to an internal interface (e.g. `10.0.0.5:8000`) and/or
  firewall it. Setting `push_consumer_allow_sources` adds matching ufw
  rules.
- Log output goes to the journal: `journalctl -u push -f`.

## Security notes

- Only token hashes are stored in the config; verification is
  SHA-256 + constant-time compare.
- `X-Forwarded-For` is honored **only** for requests sourced from
  `forwarder_ips`; all other requests are recorded by source address, so
  spoofed headers from the open internet cannot fake a client's IP. For
  forwarder-sourced requests the rightmost non-forwarder entry is used, so a
  client that prepends forged entries cannot override the source IP recorded
  by the last (trusted) hop.
- The rate limit on the client port is applied per resolved client IP
  before token checking (brute-force protection).
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
ansible/                    playbook + push role (Debian)
```
