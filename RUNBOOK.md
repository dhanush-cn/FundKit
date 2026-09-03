# Running FundKit Locally

Engineered by **Dhanush C N** ([github.com/dhanush-cn](https://github.com/dhanush-cn))

Step-by-step for Windows (PowerShell). The Docker path needs no Go, Node, Postgres, Redis or Kafka
installed on your machine — only Docker Desktop.

---

## Part 0 — Prerequisites

**You need exactly one thing: Docker Desktop.**

1. Install [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/). During
   setup, leave the **WSL 2 backend** option enabled.
2. Launch Docker Desktop and wait until the whale icon in the system tray stops animating and the
   dashboard says **Engine running**. Nothing below works until it does.
3. Open **PowerShell** and confirm:

```powershell
docker --version
docker compose version
```

Both must print a version. If `docker` is not recognised, Docker Desktop is not installed or not on
your PATH — restart the machine after installing.

**Resource check.** The stack runs 10 containers, of which Kafka and Zookeeper are JVMs. Give Docker
Desktop at least **4 GB RAM** (Settings → Resources). At 2 GB, Kafka will be killed on startup and
nothing downstream will work.

---

## Part 1 — Start the stack

```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main
docker compose up -d --build
```

**The first run takes 5–12 minutes.** It is downloading base images, compiling four Go binaries and
building the React bundle. Subsequent runs take seconds because the layers are cached.

Watch it come up:

```powershell
docker compose ps
```

Wait until every row reads `running`, and `kafka` reads `running (healthy)`. Kafka takes the longest
— roughly 45–90 seconds after its container starts — and `order-service` plus `notification-service`
deliberately wait for it.

If a service shows `restarting`, jump to [Part 5](#part-5--troubleshooting).

---

## Part 2 — Verify each layer

Run these in order. **On Windows use `curl.exe`, not `curl`** — in PowerShell, bare `curl` is an alias
for `Invoke-WebRequest` and takes different arguments.

**1. The gateway is alive**

```powershell
curl.exe http://localhost:8080/healthz
```
Expect: `{"service":"api-gateway","status":"UP","ts":"..."}`

**2. Every service is reachable and its dependencies are healthy**

```powershell
curl.exe http://localhost:8080/services/health
```
Expect `"status":"UP"` and four services each `"status":"UP"`. If one is `DOWN`, that single service
is the problem — check `docker compose logs <name>`.

**3. Dependency-level readiness (this one actually probes Postgres and Redis)**

```powershell
curl.exe http://localhost:8081/readyz
```
Expect: `{"dependencies":{"postgres":"UP","redis":"UP"},"service":"order-service","status":"READY"}`

**4. The dashboard**

Open **<http://localhost:5173>** in a browser.

**5. Metrics are being exported**

```powershell
curl.exe http://localhost:9102/metrics | findstr /B "http_requests_total"
```

Expect a handful of `http_requests_total{...}` lines. On macOS or Linux, `make metrics` does this
for all four services at once.

An empty result is not a failure — it means the service has served no traffic yet, and Prometheus
counters only appear once they have been incremented at least once. Place an order (Part 3) and
try again.

**6. Prometheus has found every target**

Open **<http://localhost:9090/targets>**. All five jobs (`prometheus`, `api-gateway`,
`order-service`, `portfolio-service`, `notification-service`) should read **UP**. A target stuck in
`DOWN` with `connection refused` means that service is not running; a `context deadline exceeded`
means it is running but wedged.

**7. The dashboard is provisioned**

Open **<http://localhost:3000/d/fundkit-overview>** (`admin` / `admin`). The panels are empty until
traffic flows, which is what Part 3 produces. Nothing needs to be imported by hand — the datasource
and the dashboard are both loaded from `monitoring/` at boot.

---

## Part 3 — Walk through the system

### Via the UI (easiest)

1. **Create an account.** On the **Create account** tab, fill in a username, your full name, an
   email address, a phone number (`+919876543210` style) and a password of at least 8 characters,
   then hit **Create account**. The gateway hashes the password with bcrypt, stores the account in
   Postgres and signs you straight in with an HS256 JWT valid for 24 hours. Returning later, the
   **Sign in** tab takes the same username and password.

   The email and phone are not decoration: they travel with every order you place and are what
   notification-service delivers to in step 4.
2. **Check the sidebar.** It should read *All services healthy*, and show *Signed in as <your name>*.
3. **Place an order.** The order is placed as the signed-in account — the *Placing as* field is
   read-only because the gateway overrides whatever the browser sends with the verified token
   subject. Leave the rest of the defaults (5000, SIP) and hit **Submit order**. It appears
   in *Latest orders* as `PENDING`, then flips to `PROCESSING` and `EXECUTED` within a couple of
   seconds — that is the background lifecycle worker, not the UI.
4. **Watch the notifications fire:**
   ```powershell
   docker compose logs -f notification-service
   ```
   You will see JSON lines with `"msg":"notification sent"`, one with `"channel":"email"` carrying
   the email address you registered with and one with `"channel":"sms"` carrying your phone number.
   Press `Ctrl+C` to stop following.
5. **Fetch P&L.** Enter `user-1` and click **Fetch P&L**. This round-trips gateway → order-service →
   portfolio-service over gRPC, with NAV served from Redis.

### Via the API (better for interviews — you can show the mechanics)

Use PowerShell's native `Invoke-RestMethod` rather than `curl.exe` for anything with a JSON body;
quoting JSON on the Windows command line is more trouble than it is worth.

```powershell
# 1. Register an account. Registering returns a session, so this is also your login.
$signup = @{
  username  = "dhanush"
  full_name = "Dhanush C N"
  email     = "dhanush@example.com"
  phone     = "+919876543210"
  password  = "correct-horse-battery"
} | ConvertTo-Json

$session = Invoke-RestMethod -Method Post -Uri http://localhost:8080/auth/register `
  -ContentType application/json -Body $signup

# Returning later, swap /auth/register for /auth/login with just username + password:
# $session = Invoke-RestMethod -Method Post -Uri http://localhost:8080/auth/login `
#   -ContentType application/json -Body (@{ username = "dhanush"; password = "correct-horse-battery" } | ConvertTo-Json)

$headers = @{ Authorization = "Bearer $($session.token)" }

# Who am I? Answered from the database, not from the token.
Invoke-RestMethod -Uri http://localhost:8080/auth/me -Headers $headers

# 2. Place an order. Note there is no user_id: the gateway supplies the verified
#    subject and ignores anything the client claims.
$order = @{
  fund_id         = "axis-bluechip"
  amount          = 5000
  type            = "SIP"
  idempotency_key = "demo-key-001"
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://localhost:8080/orders `
  -Headers $headers -ContentType application/json -Body $order

# 3. Send the SAME request again - the idempotency guard rejects it with 409
try {
  Invoke-RestMethod -Method Post -Uri http://localhost:8080/orders `
    -Headers $headers -ContentType application/json -Body $order
} catch {
  "status: " + $_.Exception.Response.StatusCode.value__   # 409
  $_.ErrorDetails.Message                                 # {"error":"duplicate order request"}
}

# 4. List orders (wait ~3s after placing to see EXECUTED)
Invoke-RestMethod -Uri http://localhost:8080/orders -Headers $headers |
  Format-Table id, fund_id, amount, status, created_at

# 5. P&L - this one crosses the gRPC hop to portfolio-service
Invoke-RestMethod -Uri "http://localhost:8080/portfolio/$($session.user_id)/pnl" -Headers $headers

# 6. No token => 401
try { Invoke-RestMethod -Uri http://localhost:8080/orders } catch {
  "status: " + $_.Exception.Response.StatusCode.value__
}

# 7. Wrong password => 401, and the message is identical to an unknown username,
#    so the API never reveals which accounts exist.
try {
  Invoke-RestMethod -Method Post -Uri http://localhost:8080/auth/login `
    -ContentType application/json `
    -Body (@{ username = "dhanush"; password = "wrong" } | ConvertTo-Json)
} catch { $_.ErrorDetails.Message }

# 8. Try to place an order as someone else - the gateway strips the spoofed header.
$spoofed = @{ Authorization = "Bearer $($session.token)"; "x-fundkit-user-id" = "victim" }
Invoke-RestMethod -Method Post -Uri http://localhost:8080/orders `
  -Headers $spoofed -ContentType application/json `
  -Body (@{ fund_id = "axis-bluechip"; amount = 1000; type = "SIP"; idempotency_key = "spoof-001" } | ConvertTo-Json) |
  Select-Object user_id   # still your own id
```

### Prove distributed tracing works

This is the demo worth rehearsing — it is the thing most portfolio projects cannot show.

```powershell
# Send a request carrying your own correlation id
$traced = @{ Authorization = "Bearer $token"; "x-request-id" = "trace-me-12345" }
$body = @{
  user_id         = "user-1"
  fund_id         = "hdfc-top-100"
  amount          = 7500
  type            = "LUMPSUM"
  idempotency_key = "trace-demo-001"
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://localhost:8080/orders `
  -Headers $traced -ContentType application/json -Body $body

# Let the lifecycle finish, then find that id across every service
Start-Sleep -Seconds 3
docker compose logs | Select-String "trace-me-12345"
```

You will see the same id in the api-gateway access log, in order-service (placed → processing →
executed), and in notification-service — proving it survived an HTTP hop, a Kafka hop, and a
detached background goroutine.

### Confirm the events are really on Kafka

```powershell
docker compose exec kafka kafka-console-consumer --bootstrap-server localhost:9092 --topic order_events --from-beginning --max-messages 5
```

### Look at the database directly

```powershell
docker compose exec postgres psql -U fundkit -d fundkit_db -c "SELECT id, user_id, amount, status FROM orders ORDER BY created_at DESC LIMIT 5;"
```

---

## Part 4 — Stopping and resetting

```powershell
docker compose down          # stop everything, keep the database
docker compose down -v       # stop and wipe the Postgres volume (fresh start)
docker compose restart order-service   # restart one service
docker compose up -d --build order-service   # rebuild one service after a code change
```

---

## Part 5 — Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `docker : command not found` | Docker Desktop not running or not installed | Start Docker Desktop, wait for "Engine running" |
| `port is already allocated` on **5432 / 6379** | A local Postgres or Redis owns the port | Already handled: the compose file publishes Postgres on **5433** and Redis on **6380**. Nothing inside the stack is affected — containers still talk to `postgres:5432` and `redis:6379`. |
| `port is already allocated` on **8080 / 3000 / 5173 / 9090** | Another dev server, Grafana or Prometheus already running | Find the owner with `netstat -ano \| findstr :8080`, then `Get-Process -Id <pid>`. Either stop it, or change the left-hand number of that port mapping in `docker-compose.yml` |
| `kafka` stuck at `starting` / never `healthy` | Not enough RAM, or slow first start | Give Docker ≥4 GB. Kafka's healthcheck allows 45s startup + 15 retries; give it 2 minutes before worrying. Then `docker compose logs kafka` |
| `order-service` restarting, logs say `FUNDKIT_DB_URL must be set` | Compose env not applied | You edited `docker-compose.yml` — confirm the `order-service` env block still has `FUNDKIT_DB_URL` |
| `api-gateway` exits with `FUNDKIT_JWT_SECRET must be at least 16 characters` | Secret shortened | It is intentional (a default signing key is a security bug). Keep the compose value or supply ≥16 chars |
| Dashboard loads but every call fails | Gateway not reachable from the browser | Check `curl.exe http://localhost:8080/healthz`. If the gateway is fine, rebuild the frontend: `docker compose up -d --build frontend` |
| `missing go.sum entry` during build | Checksum file out of date | Already handled — the Dockerfiles run `go mod tidy` before building. If you still see it, run `docker compose build --no-cache <service>` |
| Build fails downloading Go modules | Network/proxy | Retry; if you are behind a corporate proxy, configure it in Docker Desktop → Settings → Resources → Proxies |
| Orders stay `PENDING` forever | The lifecycle worker could not reach Postgres | `docker compose logs order-service` and look for `failed to mark order processing` |
| No notifications appear | Kafka not healthy, or you are watching a non-terminal event | Only `EXECUTED` and `FAILED` trigger alerts — that is deliberate. Check `docker compose logs notification-service` for `kafka consumer started` |
| Everything is weird after edits | Stale layers/volumes | `docker compose down -v` then `docker compose up -d --build` |

**The single most useful command when something is wrong:**

```powershell
docker compose logs --tail=50 <service-name>
```

---

## Part 6 — Developer mode (infrastructure in Docker, services on your machine)

Use this when you are changing Go code and don't want a Docker rebuild on every save. Requires
**Go 1.25+** ([go.dev/dl](https://go.dev/dl/)) and **Node 20+** ([nodejs.org](https://nodejs.org/)).

```powershell
go version   # must be 1.25 or newer
node -v      # must be 20 or newer
```

**Step 1 — start only the infrastructure**

```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main
docker compose up -d zookeeper kafka postgres redis
```

**Step 2 — run each service in its own PowerShell window**

Note that Kafka is reached on **29092** here, not 9092 — that is the host-facing listener. `9092` is
only routable from inside the Docker network.

*Window 1 — portfolio-service*
```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\portfolio-service
$env:FUNDKIT_REDIS_URL="localhost:6380"
go run ./cmd/server
```

*Window 2 — order-service*
```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\order-service
$env:FUNDKIT_DB_URL="postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable"
$env:FUNDKIT_REDIS_URL="localhost:6380"
$env:FUNDKIT_KAFKA_BROKERS="localhost:29092"
$env:FUNDKIT_PORTFOLIO_GRPC_URL="localhost:50051"
go run ./cmd/server
```

*Window 3 — notification-service*
```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\notification-service
$env:FUNDKIT_KAFKA_BROKERS="localhost:29092"
go run ./cmd/server
```

*Window 4 — api-gateway*
```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\api-gateway
$env:FUNDKIT_JWT_SECRET="fundkit-local-dev-secret-change-me"
$env:FUNDKIT_ORDER_SERVICE_URL="http://localhost:8081"
$env:FUNDKIT_ORDER_SERVICE_HEALTH_URL="http://localhost:8081/healthz"
$env:FUNDKIT_PORTFOLIO_SERVICE_HEALTH_URL="http://localhost:8082/healthz"
$env:FUNDKIT_NOTIFICATION_SERVICE_HEALTH_URL="http://localhost:8083/healthz"
go run ./cmd/server
```

*Window 5 — frontend*
```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\frontend
npm install
npm run dev
```

Open <http://localhost:5173>. Each service reloads on `Ctrl+C` + re-run; the frontend hot-reloads on
save.

**Graceful shutdown is worth watching.** Press `Ctrl+C` in the order-service window and read the
logs: it stops accepting connections, drains in-flight requests, waits for background lifecycle
workers, closes Postgres/Redis/Kafka, then prints `order-service stopped cleanly`. That sequence is a
good thing to be able to demo.

---

## Part 7 — Running the tests

### Unit tests (nothing needs to be running)

Every external dependency sits behind an interface with an in-memory fake, so the unit suites need
no Docker, no database and no network.

```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\api-gateway
go test ./...

cd ..\order-service
go test ./...

cd ..\portfolio-service
go test ./...

cd ..\notification-service
go test ./...
```

### Dashboard tests

```powershell
cd C:\Users\Dhanush\FundKit-main\FundKit-main\frontend
npm ci
npm run typecheck
npm run lint
npm test
```

### Integration tests (real Postgres, Redis and Kafka)

These are behind the `integration` build tag, so they never run by accident. Start the backing
services first — the stack from Part 1 is enough:

```powershell
docker compose up -d postgres redis kafka

$env:FUNDKIT_TEST_DB_URL    = "postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable"
$env:FUNDKIT_TEST_REDIS_URL = "localhost:6380"
$env:FUNDKIT_TEST_KAFKA_BROKERS = "localhost:29092"

cd C:\Users\Dhanush\FundKit-main\FundKit-main\api-gateway
go test -tags=integration -count=1 ./...

cd ..\order-service
go test -tags=integration -count=1 ./...
```

Each suite skips itself when its variable is unset, so a plain `go test ./...` stays green.

> These suites write to the `users` and `orders` tables and delete their contents between tests.
> Point them at a throwaway database, never one whose data you care about.

Or, if you have `make` available (Git Bash / WSL):

```bash
make test              # unit tests for every service
make test-frontend     # tsc, eslint and vitest for the dashboard
make test-integration  # brings up the infrastructure, then the tagged suites
make build             # compile every service
make vet               # go vet across all modules
make ci                # the local approximation of the CI pipeline
make up                # docker compose up -d --build
make down              # docker compose down
```

### Continuous integration

The same checks run in GitHub Actions on every push and pull request — see
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). If a change is green locally with
`make ci` and `make test-integration`, it will be green there.

---

## Ports reference

| Port | Service |
|---|---|
| 5173 | Frontend dashboard |
| 8080 | API gateway (the only port a client should use) |
| 8081 | order-service (HTTP) |
| 8082 | portfolio-service (HTTP probes) |
| 8083 | notification-service (HTTP probes) |
| 50051 | portfolio-service (gRPC) |
| 5433 | PostgreSQL (5432 inside the Docker network) |
| 6380 | Redis (6379 inside the Docker network) |
| 29092 | Kafka (from your machine) |
| 9092 | Kafka (inside the Docker network only) |
| 9101 | api-gateway `/metrics` (`:9100` inside the Docker network) |
| 9102 | order-service `/metrics` (`:9100` inside the Docker network) |
| 9103 | portfolio-service `/metrics` (`:9100` inside the Docker network) |
| 9104 | notification-service `/metrics` (`:9100` inside the Docker network) |
| 9090 | Prometheus |
| 3000 | Grafana (`admin` / `admin`) |

Every service serves `/metrics` on the same internal port, `9100`. The distinct host ports above
exist only so all four can be curled side by side from your machine; Prometheus scrapes `:9100`
directly over the Docker network and needs none of them published.

---

## Health endpoints reference

| Endpoint | Meaning |
|---|---|
| `/healthz` | Liveness — the process is up. Failing this restarts the pod in Kubernetes. |
| `/readyz` | Readiness — dependencies are actually reachable. Failing this only removes the pod from the load balancer. |
| `/health` | Legacy alias for `/healthz`, kept for compatibility. |
| `/services/health` | Gateway-only: probes every upstream's `/readyz` concurrently and aggregates, so the dashboard reflects dependency health rather than just live processes. |
| `/metrics` | Prometheus exposition, on the **admin port** (`:9100`), never on the traffic port. Deliberately outside the auth, CORS and rate-limit chain, and never published through an ingress. |
