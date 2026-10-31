# WebRoon — Roon Remote Web UI (Go + HTMX + Templ)

**WebRoon** is a lightweight, server-side rendered web remote controller for **Roon Server** built in pure Go using [`a-h/templ`](https://github.com/a-h/templ) and [HTMX](https://htmx.org/).

It communicates natively with your Roon Core using Roon's **SOOD** (UDP multicast/broadcast discovery on port `9003`) and **MOO/1** (bidirectional RPC over WebSocket) protocols—without requiring Node.js or external bridges.

---

## Features

- **SOOD LAN Auto-Discovery**: Automatically finds your Roon Core on the local network via UDP port `9003` (`239.255.90.90` multicast and subnet broadcast).
- **Native MOO/1 over WebSocket**: Full Go implementation of Roon's `com.roonlabs.registry:1`, `com.roonlabs.browse:1`, `com.roonlabs.transport:2`, `com.roonlabs.image:1`, `com.roonlabs.pairing:1`, and `com.roonlabs.ping:1` services.
- **Global Search (Library + TIDAL / Qobuz)**: Search across both your local Roon library and connected streaming services (**TIDAL**, **Qobuz**, **KKBOX**) for Artists, Albums, Tracks, Composers, Playlists, and Works.
- **Catalog Browsing**: Navigate *Explore & Streaming*, *Albums*, *Artists*, *Playlists*, *Genres*, *Composers*, and *Live Radio* with adaptive card-grid and track-list layouts and in-memory artwork caching.
- **Real-Time Now Playing & Transport Controls**:
  - Live Server-Sent Events (`/api/events`) pushing zone state changes, track metadata, seek bar progress, and queue updates to the browser.
  - Play/Pause, Previous, Next, Seek bar scrubbing, Shuffle, Loop (`loop`, `loop_one`, `disabled`), and Queue jump (`play_from_here`).
  - Multi-Zone switcher and per-output Volume slider (`dB`, `0-100`, or `incremental`) + Mute toggle.

---

## Project Structure

```text
├── cmd/webroon/main.go          # Application entrypoint
├── internal/
│   ├── roon/
│   │   ├── sood.go              # UDP 9003 SOOD LAN discovery
│   │   ├── moo.go               # WebSocket MOO/1 framing & subscriptions
│   │   ├── client.go            # Roon Core registration, browse, search, transport & image proxy
│   │   └── types.go             # Roon API data models
│   ├── server/
│   │   └── server.go            # HTTP routes, HTMX partial handlers, image cache & SSE stream
│   └── ui/
│       ├── views.templ          # Templ HTML components
│       └── helpers.go           # Formatting and view state helpers
├── static/styles.css            # Roon dark-mode audiophile theme
├── Dockerfile                   # Multi-stage container build (Podman & Docker compatible)
└── deploy/
    ├── deploy-podman.sh         # Bash script to build & run with Podman
    ├── deploy-podman.ps1        # PowerShell script to build & run with Podman
    ├── deploy-docker.sh         # Bash script to build & run with Docker
    ├── deploy-docker.ps1        # PowerShell script to build & run with Docker
    ├── deploy-container.sh      # Unified Bash script (auto-detects podman/docker)
    ├── deploy-container.ps1     # Unified PowerShell script (auto-detects podman/docker)
    ├── docker-compose.yml       # Compose file for Docker Compose / podman-compose
    ├── deploy-k8s.sh            # Bash script to deploy to Kubernetes
    ├── deploy-k8s.ps1           # PowerShell script to deploy to Kubernetes
    └── k8s/manifest.yaml        # Kubernetes Namespace, PVC, Deployment (hostNetwork) & Service
```

---

## Local Development

### Prerequisites
- **Go 1.24+**
- **[templ CLI](https://templ.guide/)** (`go install github.com/a-h/templ/cmd/templ@v0.3.1020`)

### Run Locally
```powershell
templ generate
go run ./cmd/webroon
```
Then open **[http://localhost:8080](http://localhost:8080)** in your browser.

### First-Time Roon Authorization
1. Open your Roon app and navigate to **Settings → Extensions**.
2. Click **Enable** next to **WebRoon Web Controller**.
3. Your pairing token is saved to `roonstate.json` (excluded from Git via `.gitignore`, configurable via `ROON_STATE_FILE`) so future restarts reconnect automatically.

---

## Deployment

### 1. Podman or Docker

You can deploy using the helper scripts in `deploy/`, which auto-detect `podman` or `docker`, build the image, create a persistent `webroon_data` volume for `/data/roonstate.json`, and run the container:

**Linux / macOS (Bash `.sh`):**
```bash
# Deploy to Podman:
./deploy/deploy-podman.sh

# Deploy to Docker:
./deploy/deploy-docker.sh

# Or auto-detect Podman/Docker:
./deploy/deploy-container.sh
```

**Windows / Cross-Platform (PowerShell `.ps1`):**
```powershell
# Deploy to Podman:
.\deploy\deploy-podman.ps1

# Deploy to Docker:
.\deploy\deploy-docker.ps1

# Or auto-detect Podman/Docker:
.\deploy\deploy-container.ps1
```

**Docker Compose / Podman Compose:**
```bash
docker compose -f deploy/docker-compose.yml up -d --build
# or
podman-compose -f deploy/docker-compose.yml up -d --build
```

> **Networking Note for SOOD Discovery:** On Linux hosts, the scripts and Compose file use `--network host` (`network_mode: host`) so UDP port `9003` multicast (`239.255.90.90`) and subnet broadcast packets reach your Roon Core on the LAN directly.

---

### 2. Kubernetes (`k8s`)

The Kubernetes manifest in [`deploy/k8s/manifest.yaml`](deploy/k8s/manifest.yaml) creates:
- Namespace `webroon`
- `PersistentVolumeClaim` (`webroon-data`) mounted at `/data` to persist `roonstate.json`
- `Deployment` (`webroon`) configured with `hostNetwork: true` and `dnsPolicy: ClusterFirstWithHostNet` so Roon SOOD LAN discovery works on your cluster node's LAN interface
- `Service` (`webroon`) exposing HTTP port `8080`

**Linux / macOS (Bash):**
```bash
# Apply manifests directly:
./deploy/deploy-k8s.sh

# Or build & push image first with podman/docker, then deploy:
IMAGE=ghcr.io/maeck70/webroon:latest BUILD_AND_PUSH=1 ./deploy/deploy-k8s.sh
```

**Windows / PowerShell:**
```powershell
# Apply manifests directly:
.\deploy\deploy-k8s.ps1

# Or build & push image first with podman/docker, then deploy:
.\deploy\deploy-k8s.ps1 -Image ghcr.io/maeck70/webroon:latest -BuildAndPush
```
