# WebRoon — Roon Remote Web UI (Go + HTMX + Templ)

A server-side rendered web remote controller for **Roon Server** built in pure Go using [`a-h/templ`](https://github.com/a-h/templ) and [HTMX](https://htmx.org/).

## Features

- **Native SOOD LAN Auto-Discovery**: Automatically discovers your Roon Core on the local network over UDP port `9003` (multicast `239.255.90.90` and subnet broadcast).
- **Native MOO/1 over WebSocket Protocol**: Full bidirectional implementation of Roon's `MOO/1` binary framing protocol (`com.roonlabs.registry:1`, `com.roonlabs.browse:1`, `com.roonlabs.transport:2`, `com.roonlabs.image:1`, `com.roonlabs.pairing:1`, `com.roonlabs.ping:1`).
- **Global Search (Library + TIDAL / Qobuz)**: Search from the top bar across both your local Roon library and connected streaming services (TIDAL, Qobuz, KKBOX) for Artists, Albums, Tracks, Composers, Playlists, and Works.
- **Full Catalog Browsing**: Browse Explore/Streaming services, Albums, Artists, Playlists, Genres, Composers, and Live Internet Radio with automatic card grid vs. track list rendering and cached artwork proxying.
- **Real-Time Now Playing & Transport Controls**:
  - Live Server-Sent Events (`/api/events`) updating Zone playback state, track metadata, seek bar progress, and queue changes in real time.
  - Play/Pause, Previous, Next, Seek scrubbing, Shuffle, Loop (`loop`, `loop_one`, `disabled`), and Queue jump (`play_from_here`).
  - Multi-Zone selector and per-output Volume slider (`dB`, `0-100`, or `incremental`) + Mute controls.

## Quick Start

1. **Generate Templ templates (if modified) and run the server**:
   ```powershell
   templ generate
   go run ./cmd/webroon
   ```
2. **Open the Web UI**:
   Navigate to [http://localhost:8080](http://localhost:8080).
3. **Authorize in Roon (first run only)**:
   - Open your Roon app and go to **Settings → Extensions**.
   - Click **Enable** next to **WebRoon Web Controller**.
   - The pairing token is automatically persisted to `roonstate.json` for future runs.
