# SHDC Field Console (`website/`)

Live operations dashboard for the cluster — vanilla HTML/CSS/JS, no build step.
It is **served same-origin by the cache nodes themselves** (embedded via
`website.go`, mounted at `/` behind the API routes), so every call is a plain
`fetch` with no proxy, CORS preflights, or mixed-content wall. A separately
hosted HTTPS page could not call the HTTP-only cluster API — that is why the
dashboard lives inside the binary, not on GitHub Pages.

## Files

```
website/
├── index.html          # All copy + sections + inline SVG art
├── css/
│   └── style.css       # Design tokens (OKLCH), editorial + ops-deck system
├── js/
│   └── app.js          # Mesh render, console, labs, telemetry, ticker
├── assets/
│   └── favicon.svg     # Node-mark icon
├── website.go          # go:embed wiring (not served as a page)
└── README.md           # This file
```

## Design register

Committed ink-moss + bone paper + rationed terracotta/brass. Three type
roles: Fraunces (display), Archivo (UI), IBM Plex Mono (data). Layout is
left-anchored and asymmetric; no card grids, no centered hero, no gradient
text, no per-section eyebrows. Motion (anime.js CDN, guarded) degrades to
instant states; `prefers-reduced-motion` disables all of it. Body text holds
≥4.5:1 contrast on both paper and ink grounds.

## Local preview

Run a node and open it — the page at `/` IS this dashboard:

```bash
go run ./cmd/cache -addr :8080 -id 127.0.0.1:8080 -peers "127.0.0.1:8081"
# open http://localhost:8080/
```

`anime.js` loads from CDN; with no network the page still works, minus motion.

## Dashboard writes

Console SETs default to a one-hour TTL so experiments clean up after
themselves. The API carries no auth by design — treat the cluster like a demo
bench, not a vault.
