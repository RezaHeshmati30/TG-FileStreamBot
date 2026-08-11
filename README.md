<div align="center">

<!-- Add the project logo as assets/logo.png, then uncomment the next line. -->
<img src="assets/logo.png" alt="TG File Stream Bot logo" width="180">

# TG File Stream Bot

**Turn Telegram files into secure, streamable links — with subtitle discovery,
external-player integration, and private access management.**

[Features](#features) · [Setup](#getting-started) · [Configuration](#configuration) · [Hugging Face](#hugging-face-spaces) · [License](#license-and-attribution)

</div>

## Overview

TG File Stream Bot is a self-hosted Telegram bot that creates temporary direct
links for files stored in Telegram. Videos can be streamed with HTTP range
requests, downloaded in a browser, or opened directly in Web Video Caster and
VLC.

The project also provides an on-demand subtitle workflow. It can inspect and
extract embedded text subtitles with FFmpeg or search Subsource for external
subtitles. A selected subtitle can be opened separately or launched together
with its video in Web Video Caster.

This repository is based on the original open-source
[EverythingSuckz/TG-FileStreamBot](https://github.com/EverythingSuckz/TG-FileStreamBot).
It is now independently maintained and extended by
[Reza Heshmati](https://github.com/RezaHeshmati30). The original authorship is
acknowledged in [License and attribution](#license-and-attribution).

## Features

### File links and streaming

- Creates direct links for Telegram documents, videos, audio, photos, and other
  supported files.
- Supports HTTP range requests for seeking and efficient video playback.
- Provides separate **Download** and **Stream** actions.
- Displays the filename, file size, creation time, and expiration time in a
  structured Telegram message.
- Uses configurable local-time display through an IANA timezone.
- Expires generated links after seven days.
- Shows user-friendly HTML pages for malformed, invalid, expired, or unavailable
  links.

### Secure links and privacy

- Protects every public file link with an HMAC signature and expiration time.
- Uses separately signed callback actions and external-player launch links.
- Rejects modified, incomplete, and expired links before streaming file data.
- Redacts signatures, tokens, authorization values, and other sensitive data
  from application logs.
- Avoids logging complete usable stream URLs.

### Embedded subtitles

- Inspects subtitle tracks only after the user requests them.
- Lists available embedded subtitle languages and track names.
- Extracts only the selected text-based subtitle track as SRT.
- Streams the source video to FFmpeg instead of first storing the complete video
  on disk.
- Applies configurable concurrency, timeout, and output-size limits.
- Clearly reports unsupported image-based formats such as PGS and VobSub.

### Online subtitle search

- Searches the Subsource API on demand.
- Detects movie or series title, year, season, and episode from common release
  filenames.
- Supports confirmation and correction of the detected movie or series.
- Filters series results by season and episode and validates the selected file
  inside downloaded subtitle archives.
- Displays results in pages to keep Telegram keyboards compact.
- Saves each user's preferred subtitle language and reuses it for future
  searches.
- Safely limits, validates, and extracts downloaded subtitle archives.
- Shows both the original video filename and the actual extracted subtitle
  filename in the final result.

### External players

- Opens a video without subtitles directly in **Web Video Caster**.
- Opens a video without subtitles directly in **VLC for iOS**.
- Opens a video together with a selected subtitle in **Web Video Caster**.
- Uses short signed HTTPS launch pages so Telegram never needs to open custom app
  schemes directly.
- Provides a manual app-launch button if automatic opening is blocked by the
  mobile browser.

### Private access management

- Restricts bot usage to approved Telegram users.
- Stores the persistent access list in a pinned message inside a dedicated
  private Telegram channel—no external database is required.
- Provides owner-only commands and a Telegram reply-keyboard administration
  menu.
- Remembers usernames alongside Telegram IDs for easier administration.
- Allows unauthorized users to display, copy, and share their Telegram ID.
- Persists users and subtitle-language preferences across application restarts.

## Bot workflow

1. An authorized user sends a file to the bot.
2. The bot copies the file to its private storage channel.
3. The bot creates a signed link with a seven-day expiration time.
4. The user can download, stream, open the video in an external player, inspect
   embedded subtitles, or search for an online subtitle.
5. Public stream requests are validated before Telegram file data is fetched.

Files remain stored in Telegram. Temporary subtitle extraction files are removed
after processing; prepared subtitle files are uploaded to the private storage
channel and served through the same signed-link mechanism.

## Getting started

### Requirements

- Go 1.25 or Docker
- A Telegram API ID and API hash from
  [my.telegram.org](https://my.telegram.org/)
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- A private Telegram channel for stored files
- A separate private Telegram channel for access-state persistence
- A public HTTPS address for generated links
- FFmpeg and FFprobe for embedded subtitle support
- An optional Subsource API key for online subtitle search

### Telegram preparation

1. Create a private storage channel and add the bot as an administrator.
2. Create a separate private access channel and add the bot as an administrator
   with permission to post, edit its own messages, and pin messages.
3. Determine both channel IDs, including the `-100` prefix.
4. Determine your numeric Telegram user ID for `OWNER_ID`.
5. Do not manually edit, delete, or unpin the `FSB_ACCESS_STATE_V1` message that
   the bot creates in the access channel.

### Run with Docker Compose

```bash
cp fsb.sample.env fsb.env
```

Fill in the required values in `fsb.env`, then run:

```bash
docker compose up --build
```

The application listens on port `8080` by default.

### Build from source

```bash
go build -o fsb ./cmd/fsb
./fsb run
```

FFmpeg and FFprobe must be available in `PATH` if embedded subtitle extraction
is required.

## Configuration

Keep credentials in secrets or environment variables. Never commit a populated
`fsb.env` file.

### Required variables

| Variable | Description |
| --- | --- |
| `API_ID` | Telegram application API ID |
| `API_HASH` | Telegram application API hash |
| `BOT_TOKEN` | Token created with BotFather |
| `LOG_CHANNEL` | Private storage-channel ID |
| `ACCESS_CHANNEL` | Dedicated private access-state channel ID |
| `OWNER_ID` | Telegram user ID allowed to administer access |
| `LINK_SIGNING_KEY` | Secret signing key with at least 32 characters |
| `HOST` | Public base URL, for example `https://example.com` |

### Optional variables

| Variable | Default | Description |
| --- | ---: | --- |
| `SUBSOURCE_API_KEY` | — | Enables online subtitle search |
| `TIMEZONE` | `Europe/Berlin` | IANA timezone used in bot messages |
| `PORT` | `8080` | HTTP server port |
| `DEV` | `false` | Enables development mode |
| `ALLOWED_USERS` | — | Comma-separated initial user IDs for a new access channel |
| `USE_SESSION_FILE` | `true` | Reuses worker session files |
| `USER_SESSION` | — | Optional Telegram user-session string |
| `USE_PUBLIC_IP` | `false` | Attempts public-IP host discovery |
| `STREAM_CONCURRENCY` | `4` | Parallel Telegram chunk downloads |
| `STREAM_BUFFER_COUNT` | `8` | Number of prefetched stream chunks |
| `STREAM_TIMEOUT_SEC` | `30` | Per-chunk timeout in seconds |
| `STREAM_MAX_RETRIES` | `3` | Retry count for failed chunks |
| `SUBTITLE_CONCURRENCY` | `1` | Simultaneous subtitle extractions |
| `SUBTITLE_PROBE_TIMEOUT_SEC` | `60` | FFprobe timeout in seconds |
| `SUBTITLE_EXTRACT_TIMEOUT_SEC` | `1800` | FFmpeg extraction timeout in seconds |
| `SUBTITLE_MAX_OUTPUT_MB` | `20` | Maximum extracted subtitle size |

See [`fsb.sample.env`](fsb.sample.env) for a complete example.

Generate `LINK_SIGNING_KEY` with a cryptographically secure random generator.
Do not reuse `BOT_TOKEN`, `API_HASH`, or another service credential as the
signing key. Changing this value immediately invalidates previously generated
links.

## Access administration

The following commands are available in private chats:

| Command | Availability | Purpose |
| --- | --- | --- |
| `/id` | Everyone | Displays the caller's Telegram profile and ID |
| `/allow <user-id>` | Owner | Grants access |
| `/deny <user-id>` | Owner | Revokes access |
| `/users` | Owner | Lists authorized users and known usernames |

The owner also receives an administration reply keyboard for the same access
management operations.

## Hugging Face Spaces

The repository contains a lightweight Hugging Face bootstrap deployment in
[`deploy/hugging-face`](deploy/hugging-face). The Space stores only its small
Docker bootstrap files and clones the latest `main` branch from GitHub during a
rebuild.

1. Create a Docker Space.
2. Copy the contents of `deploy/hugging-face` into the Space repository.
3. Add all required credentials as **Secrets**, not public variables.
4. Set `HOST` to the public HTTPS URL of the Space.
5. Rebuild or restart the Space after pushing changes to GitHub.

The deployment image already includes FFmpeg. Detailed notes about access-state
and subtitle configuration are available in
[`deploy/hugging-face/README.md`](deploy/hugging-face/README.md).

## Notable changes from the upstream project

This independently maintained version retains the original Telegram streaming
foundation and adds or substantially expands:

- structured file-link messages with filename, size, local time, and expiration;
- seven-day HMAC-signed URLs and signed Telegram callback actions;
- retryable buffered streaming and reduced sensitive request logging;
- user-friendly HTML error pages;
- persistent private-channel access management and an owner administration UI;
- rich Telegram ID/profile cards with copy and share actions;
- on-demand embedded subtitle inspection and FFmpeg extraction;
- Subsource search with saved language preferences and series-aware filtering;
- secure subtitle archive handling and actual subtitle filename reporting;
- Web Video Caster integration for video-only and video-plus-subtitle playback;
- VLC video launch integration;
- Docker and Hugging Face bootstrap deployment documentation.

## Security notes

- Treat `BOT_TOKEN`, `API_HASH`, `USER_SESSION`, `SUBSOURCE_API_KEY`, and
  `LINK_SIGNING_KEY` as secrets.
- Keep both Telegram channels private.
- Do not expose a development server directly to the internet.
- Use HTTPS for `HOST` in production.
- Rotate a compromised signing key and restart the service; existing links will
  become invalid.
- Anyone who receives a valid file link can use it until it expires. Share links
  accordingly.

## Limitations

- Link availability depends on Telegram, the bot workers, and the hosting
  service remaining available.
- Embedded subtitle extraction supports text-based tracks only.
- Large videos may take time to scan because the selected subtitle track can be
  located near the end of the container.
- External-player deep links require the corresponding mobile app and may first
  open a browser fallback page.
- Online subtitle availability and metadata quality depend on Subsource.

## Credits

- [EverythingSuckz](https://github.com/EverythingSuckz) and contributors for the
  original TG-FileStreamBot codebase.
- [celestix/gotgproto](https://github.com/celestix/gotgproto) for the Telegram
  client framework.
- [divyam234/teldrive](https://github.com/divyam234/teldrive) for related
  Telegram-drive work credited by the upstream project.
- [krau](https://github.com/krau) for image-support work credited by the upstream
  project.
- [FFmpeg](https://ffmpeg.org/) for media probing and subtitle extraction.
- [Subsource](https://subsource.net/) for online subtitle discovery.

## License and attribution

This project is free software licensed under the
[GNU Affero General Public License, version 3 or later](LICENSE).

The repository is a modified and independently maintained derivative of
[EverythingSuckz/TG-FileStreamBot](https://github.com/EverythingSuckz/TG-FileStreamBot).
Removing GitHub's fork relationship does not change that origin or the license
obligations, and this README intentionally preserves that attribution.

```text
Copyright (C) 2026 EverythingSuckz and original contributors
Copyright (C) 2026 Reza Heshmati — modifications and continued development
```

The original authors retain copyright in their contributions. Reza Heshmati
retains copyright only in the subsequent modifications and original additions.
No claim is made that the entire project was created from scratch by the current
maintainer.

If you modify or operate this software over a network, review the AGPL-3.0 terms,
including the source-availability requirements. The complete license text is
provided in [`LICENSE`](LICENSE) and must remain with redistributed copies.
