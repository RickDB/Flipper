# 🐬 Flipper

Flipper is a small self-hosted web frontend that bridges [Spotweb](https://github.com/spotweb/spotweb)
and [SABnzbd](https://sabnzbd.org/): paste a Spotweb release link, pick a
SABnzbd category, and Flipper fetches the NZB and hands it straight to
SABnzbd — so SABnzbd never needs direct network access to your Spotweb
instance.

Single Go binary, SQLite database (pure Go driver, no CGO), ocean/dolphin
themed UI. Default port **19012**.

## Features

- Single admin account plus any number of additional regular users.
- Admin configures the SABnzbd connection, tests it, and picks which
  categories users are allowed to send releases to — pulled live from the
  SABnzbd API.
- Admin configures the Spotweb connection (base URL, fallback API key,
  configurable NZB download URL template) with a connection test.
- Regular users paste a Spotweb spot URL (e.g.
  `https://spotweb.example.com/#/?page=getspot&messageid=...`), pick a
  category, and send it to SABnzbd — with clear success/failure feedback.
- Each user can optionally set their own personal Spotweb API key on their
  Account page, which takes priority over the admin's fallback key —
  downloads then count against that user's own Spotweb account.
- Last 10 send attempts (success and failure) are kept as history.
- Admin can share local folders with specific users under a name of the
  admin's choosing (independent of the real folder path). Assigned users
  get an inline browser right on the dashboard — no page reloads — with
  breadcrumb navigation, single-file downloads, whole-folder zip downloads,
  and automatic refresh so newly-added files just show up.

## Container image

Pushed to GHCR on every push to `main` (and on `v*` tags) by
[`.github/workflows/docker-publish.yml`](.github/workflows/docker-publish.yml):

```
ghcr.io/rickdb/flipper:latest
ghcr.io/rickdb/flipper:0.01
```

GHCR packages built from a workflow's default `GITHUB_TOKEN` are private by
default even in a public repo — after the first successful run, go to the
package's settings on GitHub and flip it to public if you want anonymous
`docker pull` to work.

## Quick start — Docker Compose

```bash
cp .env.example .env
# edit .env: at minimum set FLIPPER_INITIAL_PASSWORD to something long & random
docker compose up -d --build
```

Flipper will be reachable at `http://<host>:19012`. On first boot, if
`FLIPPER_INITIAL_USERNAME` / `FLIPPER_INITIAL_PASSWORD` are set (the Compose
default), that admin account is created automatically. If you'd rather set
the admin account up by hand, leave those unset and open the app — you'll
land on a one-time `/setup` page instead.

The SQLite database lives at the path set by `FLIPPER_DATA_PATH` on the host
(default `./data/flipper`), bind-mounted into the container.

## Configuration

All settings are environment variables, prefixed `FLIPPER_`:

| Variable | Default | Purpose |
|---|---|---|
| `FLIPPER_LISTEN` | `:19012` | Address the HTTP server listens on |
| `FLIPPER_DB` | `data/flipper.db` | Path to the SQLite database file |
| `FLIPPER_INITIAL_USERNAME` | *(unset)* | Bootstrap admin username (first boot only) |
| `FLIPPER_INITIAL_PASSWORD` | *(unset)* | Bootstrap admin password (first boot only, 8+ chars) |

Everything else — SABnzbd URL/API key/categories, Spotweb URL/credentials —
is configured from the Admin panel in the UI and stored in the database.

## Manual / bare-metal build

Requires Go 1.23+.

```bash
go build -o flipper ./cmd/flipper
FLIPPER_LISTEN=:19012 FLIPPER_DB=./data/flipper.db ./flipper
```

Then open `http://localhost:19012` and follow the setup wizard.

Reset the admin password if you get locked out:

```bash
./flipper -reset-admin-password 'a-new-long-password'
```

Print the running version:

```bash
./flipper -version
```

## Spotweb API key

Flipper extracts the `messageid` from whatever spot URL you paste — it
understands both plain query strings and Spotweb's `#/?page=getspot&...`
hash-fragment links. It then fetches the NZB itself via Spotweb's built-in
Newznab-compatible API (the same mechanism tools like Sonarr/NZBHydra use),
using a configurable URL template (Admin → Spotweb → "NZB download URL
template") that substitutes `{base}`, `{messageid}`, and `{apikey}`. The
default,

```
{base}/api?t=get&id={messageid}&apikey={apikey}
```

matches a standard Spotweb install and needs no session login or cookies —
just a valid API key. Get your personal key from your own Spotweb profile
/ account settings page ("Gebruiker wijzigen") and set it on Flipper's
Account page; or, as the admin, set a shared fallback key under Admin →
Spotweb so the tool works before anyone has set up their own. If the
template is wrong for your instance, the connection test and a real submit
attempt will tell you quickly (Flipper refuses to forward anything that
looks like an HTML error page instead of an NZB, and will say so).

## Local folder shares

Under Admin → Local folder shares, an admin can add any folder Flipper can
see on disk and give it a display name — the name is what users see; the
real path never is. Each share can then be assigned to specific regular
users (checkboxes, same pattern as SABnzbd categories). Admins always see
every share.

Assigned users get a "Shared folders" card directly on their dashboard: a
tab per share, an inline directory browser (breadcrumbs, click a folder to
go in, click a file's download link to grab it, or "Download this folder as
.zip" for the whole thing). The listing re-fetches itself every 15 seconds,
so a file dropped into a shared folder on disk shows up without anyone
reloading the page — there's no filesystem watcher, just a cheap poll.

Path handling is defensive: every browse/download/zip request re-resolves
the requested path against the share's root and rejects anything that would
escape it (`../..` and friends), regardless of what the client sends.

Running under Docker Compose, the share path you configure in the Admin
panel is the path **inside the container**, so bind-mount the host folder
first — see the commented example in [`compose.yaml`](compose.yaml).

## Self-signed certificates

Both the SABnzbd and Spotweb connection settings have a "skip TLS
certificate verification" checkbox for self-hosted instances using
self-signed certs. Leave it unchecked unless you need it.

## Notes on this build

- Persistence is SQLite via `modernc.org/sqlite` (pure Go, no CGO, no
  separate database container needed).
- Passwords are hashed with PBKDF2-HMAC-SHA256 (stdlib only, no extra
  crypto dependency).
- Sessions are in-memory and cookie-based; restarting Flipper signs
  everyone out.
- There is intentionally only one admin account. Everyone else created
  from the Admin panel is a regular user.

## Version

Current version: **0.02**
