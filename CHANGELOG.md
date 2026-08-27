# Changelog

## Unreleased

- No unreleased changes yet.

## 0.05 — 2026-08-27

- Users can delete individual history entries they own or clear all of their
  history; admins can delete any entry or clear history for every user.
- A dedicated Downloads card now shows live SABnzbd state, percentage done
  and left, remaining size, and estimated time for Flipper-submitted jobs.
- Tracked downloads can be cleared individually or in bulk without changing
  or cancelling the underlying SABnzbd jobs.

## 0.04 — 2026-08-27

- Improved shared-folder action alignment and spacing, with a stronger
  irreversible-delete warning to help prevent accidental removal.

## 0.03 — 2026-08-27

- Share access can now grant individual users permission to permanently
  delete files and folders, with server-side authorization and path checks.
- The shared-folder ZIP download action now uses a compact listing footer
  instead of an oversized pill button.

## 0.02 — 2026-08-27

- SABnzbd uploads now use the Spotweb release title instead of the Spotnet
  message ID, while preserving spaces in the displayed release name.
- Release names in recent history now link to their submitted Spotweb URL.
- Per-user personal Spotweb API key (Account page), with an admin-level
  fallback key so Flipper works before anyone sets their own.
- Fixed NZB fetching to use Spotweb's real built-in Newznab-compatible API
  (`{base}/api?t=get&id={messageid}&apikey={apikey}`) instead of an assumed
  URL that only worked against session-authenticated HTML pages.
- Fixed existing installs never picking up the corrected NZB template above:
  it was only used for brand-new databases, so upgraded installs kept the
  old, broken guess in their settings row forever. Boot now auto-heals it
  in place (only when it's still exactly the old broken value, so a
  manually customized template is left alone).
- Send confirmations and history now show the real Spotweb release title
  (fetched via `t=details`) instead of SABnzbd's internal job ID.
- Recent history now keeps up to 100 items (was 10), paginated 10 per page.
- Dashboard is now a two-column layout: "Send a release" and "Recent
  history" on the left, "Shared folders" on the right, so the shares
  browser no longer pushes the page into a long vertical scroll.
- New: admin-managed local folder shares. Admins name and assign folders to
  specific users; assigned users get an inline browser on the dashboard
  (breadcrumbs, file downloads, whole-folder zip downloads, auto-refresh)
  with path-traversal-safe access on every request.

## 0.01 — 2026-08-27

Initial release.

- Single admin account + additional regular user creation.
- Admin panel: SABnzbd connection settings with live "test connection" and
  live category selection (multi-select) read from the SABnzbd API.
- Admin panel: Spotweb connection settings (base URL, optional HTTP basic
  auth, configurable NZB download URL template) with "test connection".
- Regular user dashboard: paste a Spotweb spot URL, pick an allowed
  category, send to SABnzbd, with clear success/failure feedback.
- Last 10 send attempts kept as history.
- SQLite persistence (pure Go driver, no CGO).
- Dolphin/ocean themed UI, original artwork.
- Docker image + Compose file, default port 19012.
