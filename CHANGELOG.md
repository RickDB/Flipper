# Changelog

## Unreleased

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
- New: admin-managed local folder shares. Admins name and assign folders to
  specific users; assigned users get an inline browser on the dashboard
  (breadcrumbs, file downloads, whole-folder zip downloads, auto-refresh)
  with path-traversal-safe access on every request.

## 0.01

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
