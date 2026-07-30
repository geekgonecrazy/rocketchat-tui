# Running a Rocket.Chat server for rctui

Two options, both needing Docker on the machine you run them from.

## 1. Compose (recommended for day-to-day use)

`docker-compose.yml` here is a trimmed version of the official file at
<https://go.rocket.chat/i/docker-compose.yml>. The official one also ships
Traefik, Prometheus exporters and Let's Encrypt wiring, and it hard-fails
without `LETSENCRYPT_EMAIL` set — none of which helps on a laptop. This one runs
just MongoDB and Rocket.Chat, exposed directly on port 3000.

```sh
cd deploy
cp .env.example .env          # optional; defaults work as-is
docker compose up -d
docker compose logs -f rocketchat    # first boot takes a minute or two
```

Then point the client at it:

```sh
cd ..
go build -o rctui .
./rctui -server http://localhost:3000
```

Log in as `admin` / `changeme123` (or whatever you set in `.env`). The admin is
seeded on first boot and the setup wizard is pre-marked complete, so there is no
need to visit the web UI first.

To use the official file instead, note that it needs at least:

```sh
DOMAIN=localhost LETSENCRYPT_EMAIL=you@example.com \
  docker compose -f docker-compose.official.yml up -d
```

and it serves through Traefik on port 80 rather than 3000.

## 2. Testcontainers (for automated tests)

`test/integration` boots a real MongoDB replica set and Rocket.Chat per test
run, then drives the app core against them. It lives in its own Go module so the
client's dependency tree stays small, and it is behind a build tag so it never
runs by accident:

```sh
cd test/integration
go test -tags integration -timeout 20m -v
```

Without a Docker endpoint the tests skip rather than fail. `DOCKER_HOST` is
honoured, so a remote daemon or a Testcontainers Cloud endpoint works too.

These tests are a drift check against the real server's wire format. The
Docker-free suite at the repo root (`go test ./...`) covers the same behaviour
against `internal/fakerc` and is what should run in CI.

## Notes

- Rocket.Chat requires MongoDB to be a **replica set**, even single-node. A
  plain `mongod` will start but Rocket.Chat refuses to use it.
- MongoDB 5.0+ requires a CPU with **AVX**. On older or low-power chips
  (Atom/Celeron Jasper Lake, some VPS instance types) `mongod` dies with
  "Illegal instruction".
- `ROOT_URL` should match the address you connect to. A mismatch is the usual
  cause of websocket connections being rejected, which shows up in rctui as a
  status bar stuck on `disconnected` while REST calls still work.
