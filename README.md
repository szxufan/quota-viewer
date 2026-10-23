# Quota Viewer

**[English](README.md)** | [简体中文](README.zh-CN.md)

A desktop floating ball + AI platform quota monitoring tool: displays API quota / balance remaining for various AI platforms in real time.

![Floating Ball](docs/screenshots/preview-1.png)
![Expanded Panel](docs/screenshots/preview-2.png)

- **Tech Stack**: Go + Wails v2.12.0 + Vanilla HTML/CSS/JS (Vite bundling)
- **Platform**: Windows 10+ (WebView2 Runtime)

## Features

- **Floating Ball**: 60×60 glassmorphic square, 1-3 cells showing enabled providers (cell color = status: green / yellow / red), hover tooltip for per-platform details
- **Adaptive Cells**: Enable 1 provider → letter fills the entire ball; 2 → each takes 1/2; 3 → each takes 1/3
- **Expandable Panel**: Progress bars + remaining quota details (thousands-separated numbers), auto-refresh (default 15 min), refresh countdown timer (e.g. Kimi 5-hour window reset)
- **Budget Progress Bar**: Balance-type providers (e.g. DeepSeek) support a configurable budget — progress bar shows consumption as `(budget - balance) / budget`, default budget ¥300
- **Config Panel**: Check which providers to display (**max 3, min 1**), each provider has its own credential inputs (API Key / Cookie), supports pasting browser "Copy as PowerShell" format directly
- **System Tray**: Refresh / Show-Hide / Open Config / Quit; double-click tray icon to toggle visibility
- **Window Positioning**: Expand without going off-screen (smart corner flipping, multi-monitor / DPI scaling adaptive), collapse precisely back to original position
- Close to tray, does not exit the process

## Supported Providers

| Provider | Credential | Displays |
|----------|------------|----------|
| Kimi | API Key (`sk-kimi-xxx`) | 5-hour window / weekly quota usage |
| Xfyun (讯飞星辰) | Cookie | Package total usage |
| OpenCode Go | API Key (blank = auto-read `~/.local/share/opencode/auth.json`) | Rolling / weekly / monthly window quota percentage |
| Xiaomi MiMo | Cookie | Package token usage |
| DeepSeek | API Key (`sk-...`) | Account balance (balance type) |
| GLM | Token (+ optional Organization/Project ID) | Coding plan window usage |
| OpenRouter | API Key (`sk-or-...`) | Credits usage |
| Bailian (百炼) | bl CLI global login (credentialless) | Token plan usage |
| New API | BaseUrl + channel_id + User + Authorization | Account balance and used amount |
| Aliyun (阿里云) | AccessKey ID + Secret (+ optional resource packages) | Account balance / package usage |
| Ollama Cloud | Cookie | Session (5h) / weekly usage |
| Command Code | API Key (blank = auto-read `~/.commandcode/auth.json`) | 5h / weekly window + monthly credits |
| Factory Droid | API Key (blank = auto-read `~/.factory/.env`) | Standard/Core 5h / weekly / monthly windows |
| MiniMax | Token Plan Key (`sk-cp-...`) | Plan card usage (multi-card, one cell each) |
| Open Design | Cookie | 5h / 7-day window usage |

> Tip: For Cookie-based providers, you can paste the entire "Copy as PowerShell" script from your browser's F12 → Network tab, and it will be automatically parsed into a Cookie header on save.

## Headless Probe (CLI)

```bash
go build -o quota-viewer-cli.exe ./cmd/quotaviewer
./quota-viewer-cli.exe --json   # one round of fetches, single-line JSON to stdout, exit 0
```

Failure envelope: argument/config failures still print a valid single-line JSON (with the `error` field) and exit 0, plus a diagnostic line on stderr; missing/unknown args print usage to stderr and exit 2.

## Development

```bash
# Install dependencies
go mod tidy
cd frontend && npm install && cd ..

# Development mode (hot reload)
wails dev

# Build
wails build
```

Output: `build/bin/quota-viewer.exe`

## Auto Update & Release

The app checks for updates automatically (30s after launch, then every 6h) and upgrades itself silently via the NSIS installer hosted on Aliyun OSS. See [`docs/AUTO_UPDATE.md`](docs/AUTO_UPDATE.md) for the release workflow (`release.env` + `go run ./cmd/release`).

## Project Structure

```
├── app.go               # Wails app logic (window positioning, config, refresh orchestration)
├── main.go              # Entry point + Wails runtime options (frameless / AlwaysOnTop)
├── workarea_windows.go  # Win32 helpers (workarea query, DPI, min window width subclass)
├── workarea_other.go    # Non-Windows stub
├── internal/
│   ├── config/          # Config persistence (dynamic provider list + PowerShell cookie parsing)
│   ├── fetcher/         # Provider registry + per-platform fetchers
│   └── tray/            # System tray (energye/systray)
└── frontend/
    ├── src/
    │   ├── index.html   # Floating ball + detail panel + config panel + Toast
    │   ├── main.js      # View state machine / dynamic ball cells / data rendering / interactions
    │   └── style.css    # Design tokens + dark glassmorphic theme
    └── dist/            # Vite build output (embedded into exe via go:embed)
```

## Adding a New Provider

Quota Viewer uses a registry-driven architecture — adding a new provider requires only a fetcher implementation + a registry entry, with **zero frontend changes**.

If you want your AI agent to add a new provider, copy and paste this to it:

> Read `docs/ADDING_A_PROVIDER.md` and follow it to add a new provider for the platform `<name>`. The endpoint is `<URL>`, authentication is `<method>`, and it should display `<what to show>`.

For manual steps, see [`docs/ADDING_A_PROVIDER.md`](docs/ADDING_A_PROVIDER.md).

## Disclaimer

This is an **unofficial** tool, not affiliated with any platform. Platform web structures and APIs may change at any time, causing fetchers to fail. Credentials are stored only in the local config file (`%APPDATA%/quota-viewer/config.json`) and are never uploaded to any server. Use at your own risk.

## License

[MIT](LICENSE)
