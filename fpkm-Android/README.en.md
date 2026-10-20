# FPKM · FeiNiu Package Manager

[中文文档](README.md) · **English**

<p align="center">
  <img src="docs/banner.png" alt="FPKM × FNOSP" width="760">
</p>

<p align="center">
  <a href="https://github.com/FNOSP/FPKM/stargazers"><img src="https://img.shields.io/github/stars/FNOSP/FPKM?style=flat-square&logo=github&label=Stars&color=2b6cff" alt="Stars"></a>
  <a href="https://github.com/FNOSP/FPKM/network/members"><img src="https://img.shields.io/github/forks/FNOSP/FPKM?style=flat-square&logo=github&label=Forks&color=2b6cff" alt="Forks"></a>
  <a href="https://github.com/FNOSP/FPKM/issues"><img src="https://img.shields.io/github/issues/FNOSP/FPKM?style=flat-square&logo=github&label=Issues&color=e3b341" alt="Issues"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/FNOSP/FPKM?style=flat-square&label=License&color=e05d44" alt="License"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=black" alt="React">
  <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20Windows%20%7C%20macOS-2b6cff?style=flat-square&logo=linux&logoColor=white" alt="Platform">
</p>

<p align="center">
  <b>Run <code>.fpk</code> offline packages on any Linux — no fnOS required</b>
</p>

---

FPKM (**F**eiNiu **P**ac**K**age **M**anager) is a `.fpk` package manager written
in Go. It reimplements the fnOS application runtime, so packages built for the
fnOS ecosystem can be installed and run on ordinary Linux distributions,
containers, or even a developer machine.

```
Your Arch / Ubuntu / Debian / CentOS  …  runs .fpk packages
```

![FPKM desktop](docs/screenshots/desktop.webp)

## Why FPKM

fnOS applications are distributed as `.fpk` packages, and a package is more than
an archive — it is a complete runtime contract. `manifest` holds metadata,
`cmd/main` manages the lifecycle, `app.tgz` carries the program itself, and at
runtime the app expects a family of `TRIM_*` environment variables, fixed paths
like `/var/apps/{appname}`, and a shared Unix-socket gateway.

That contract only exists on fnOS. Away from it:

- Extracting the `.fpk` and running the binary simply fails — missing
  environment variables, missing paths, missing gateway.
- Many applications hard-code their install path and socket location inside the
  binary itself.
- The gateway authenticates requests and injects user identity headers; without
  it, applications refuse to serve.

**FPKM's job is to rebuild that runtime faithfully on ordinary Linux.**

## Key features

### Complete `.fpk` lifecycle

```bash
fpkm install  app.fpk      # install
fpkm start    myapp        # start
fpkm status   myapp        # status
fpkm stop     myapp        # stop
fpkm upgrade  app.fpk      # upgrade (keeps data)
fpkm uninstall myapp       # uninstall
fpkm list                  # list installed
```

### Automatic entry-point detection

Packages vary wildly inside. FPKM detects the entry type automatically:

| Type | Notes |
|------|-------|
| Native binary | ELF under `bin/`, the most common case |
| `cmd/main` script | The script manages the process and PID file |
| Node.js | Runs `npm install --omit=dev` to fetch dependencies |
| Python | Invokes `python3` (dependencies must be provided) |
| Static site | Built-in static file server |
| Docker | Detects and reports the Rootless Docker requirement |

### fnOS path compatibility (the critical piece)

Many fnOS binaries **hard-code** fnOS paths and cannot be patched. FPKM creates
symlinks at install/start time so those paths resolve to the real locations:

```
/var/apps/{appname}                       → install directory
/vol1/@appdata/{appname}                  → data directory
/vol1/@appcenter/{appname}                → program directory
```

Install scripts (`cmd/main` and friends) also have their hard-coded paths
rewritten, so the original scripts run unmodified.

### Unified gateway

Reimplements the fnOS gateway: routes `/app/{appname}` requests (HTTP,
WebSocket, FastCGI) to the Unix socket an application listens on.

- Pure Go, **zero external dependencies** — no nginx needed
- **Protected by FPKM login**: unauthenticated `/app/*` requests are redirected
  to the login page, so applications cannot be reached anonymously
- Injects `X-Trim-Userid` / `X-Trim-Username` / `X-Trim-Isadmin` identity headers
- Waits for the socket to become ready before declaring success, and reports
  diagnosable reasons on timeout

### Self-contained data

Everything lives in a `data/` directory **next to the executable**:

```
./fpkm
./data/
├── apps/{appname}/    installed applications
├── password.json      admin password (PBKDF2 salted)
├── sources.json       application sources
└── logs/
```

Data follows the binary, not `$HOME`. Switching users, running with `sudo`, or
moving into a container all resolve to the same data — no more "started as a
different user, all apps vanished".

### Cross-platform

Runs on Windows, macOS, and Linux (`fpkm.exe` / `fpkm`). Architecture detection
covers `amd64` and `arm64`, and binaries are validated for platform compatibility
before installation.

## Two ways to use it

### Command line

```bash
./fpkm install  ~/Downloads/fnos-quark.fpk
./fpkm start    fnos-quark
./fpkm status   fnos-quark
```

### Web interface (desktop shell)

```bash
./fpkm web --bind 0.0.0.0 --port 7666
```

Open a browser and you get a full desktop environment with a built-in app store
and application management:

- Browse remote sources and install with one click
- Install from a local file or upload a `.fpk`
- Applications open in a sub-window (`iframe`) or a new tab, per the app's own config
- Per-app access mode override: **direct port / unified gateway / CGI**

**App store** — browse sources, install in one click, open installed apps directly:

![App store](docs/screenshots/store.webp)

**Multiple windows** — several apps open side by side, freely draggable and
resizable, with the taskbar always on top:

![Multiple windows](docs/screenshots/windows.webp)

**Login protection** — set an admin password on first visit, log in afterwards:

<img src="docs/screenshots/login.webp" alt="Login" width="760">

## Docker deployment

Image: **`qilinzhu/fpkm`** (supports `linux/amd64` / `linux/arm64`)

### docker run

```bash
docker run -d \
  --name fpkm \
  -p 7666:7666 \
  -v /path/to/fpkm-data:/app/data \
  --restart unless-stopped \
  qilinzhu/fpkm:latest
```

### docker compose

```yaml
services:
  fpkm:
    image: qilinzhu/fpkm:latest
    container_name: fpkm
    ports:
      - "7666:7666"
    volumes:
      # Required: without this you lose installed apps and the password on recreate
      - ./fpkm-data:/app/data
    restart: unless-stopped
```

Save as `compose.yaml`, then:

```bash
docker compose up -d
```

Either way, open `http://<host-ip>:7666` — the first visit asks you to set an
admin password.

> **The data volume is mandatory.** Applications, the password, and sources all
> live under `/app/data`; without a mount, `docker rm` wipes everything.

The image is based on `debian-slim`, not alpine — some ELFs inside real `.fpk`
packages link against glibc, which fails outright under alpine's musl.
See [Docker deployment](docs/DOCKER.md) for details.

## Quick start

### Build from source

Requires Go 1.24+ and Node.js (Node.js only for building the web UI).

```bash
git clone https://github.com/FNOSP/FPKM.git fpkm
cd fpkm

# Build the frontend (assets are embedded into the binary)
./scripts/build-web.sh

# Build the binary
./build.sh native
```

The output is `dist/fpkm`, and `data/` is created next to it automatically.

### Run

```bash
cd dist
./fpkm web --bind 0.0.0.0 --port 7666
```

The first visit asks you to set an admin password; after that you can install
applications.

## Known limitations

FPKM aims to run real `.fpk` packages wherever possible, but the fnOS ecosystem
contains vendor-private mechanisms. These are the known boundaries:

| Case | Status |
|------|--------|
| Hard-coded fnOS paths | ✅ Handled automatically via symlinks |
| Requires fnOS gateway signature | ⚠️ Some apps verify a private fnOS signature; use **direct port** mode instead |
| Depends on fnOS system services | ⚠️ A few apps (e.g. calling fnOS APIs) cannot be fully emulated |
| Rootless Docker | ⚠️ Docker-based apps need a runtime you provide |

When an application fails to start, the web UI reports the reason directly
(missing `manifest`, insufficient permissions, socket timeout, and so on) rather
than failing silently.

## Project layout

```
cmd/                CLI commands (cobra)
internal/
├── app/            lifecycle: install, start, process, entry detection, path compat
├── fpk/            .fpk parsing: manifest, extraction, i18n, arch validation
├── gateway/        unified gateway: HTTP / WebSocket / FastCGI reverse proxy
├── web/            web server and REST API
├── config/         configuration and data directory
├── session/        HMAC session tokens
└── logger/         logging
web/                React + TypeScript + Semi Design frontend
scripts/            build scripts
```

## Security

FPKM installs and runs arbitrary programs — it is equivalent to executing code
on the host.

- Only install `.fpk` packages from sources you trust
- Never expose the admin port directly to the internet; use a reverse proxy with HTTPS
- The admin password is stored salted with PBKDF2-HMAC-SHA256, but still use a strong one

## Compatibility & test results

FPKM cannot guarantee that every `.fpk` package runs perfectly — the fnOS
ecosystem contains vendor-private mechanisms, and applications are implemented in
very different ways. **Which apps work, and which need workarounds**, cannot be
covered by one person's testing, so we maintain a public spreadsheet of real-world
results:

### 📋 [FPKM application compatibility list](https://www.kdocs.cn/l/ceGUrJxQZoOM)

It records how each application actually behaves: whether it installs, whether it
starts, which access mode works, and any issues plus their solutions. Check it
before installing to save yourself some detours.

### Contributions welcome

If you have successfully (or unsuccessfully) run a `.fpk`, please report it in
[Issues](https://github.com/FNOSP/FPKM/issues) — a couple of data points is
genuinely useful to everyone who comes after you.

Please include:

| Item | Notes |
|------|-------|
| App name & version | e.g. `fnos-quark 0.7` |
| System environment | Distro and version (e.g. `Ubuntu 24.04` / `Debian 12`), CPU architecture |
| FPKM version | Output of `./fpkm version` |
| Result | ✅ works / ⚠️ partially works / ❌ does not run |
| Access mode | Unified gateway / direct port / CGI |
| Issues & logs | Error messages, relevant excerpts from `data/logs/` |
| Workaround | If you got it working, describe how |

> Please search for an existing report first to avoid duplicates.
> Fixes to inaccurate entries in the spreadsheet are also welcome.

If you hit a problem, also check the known boundaries in
[Known limitations](#known-limitations) and the specific error the web UI
reports.

## Acknowledgements

fnOS defined the `.fpk` distribution format and its runtime contract. FPKM
simply reimplements that contract outside of fnOS.

## License

[Apache License 2.0](LICENSE) © 2026 FPKM contributors

Free to use, modify, and redistribute (including commercially), provided the
copyright and license notices are retained.
