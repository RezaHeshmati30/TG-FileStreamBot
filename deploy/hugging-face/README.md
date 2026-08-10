---
title: TG File Stream Bot
emoji: 📁
colorFrom: blue
colorTo: indigo
sdk: docker
app_port: 8080
---

This Space builds the latest `main` branch from
`RezaHeshmati30/TG-FileStreamBot` on GitHub.

## Private access management

Create a separate private Telegram channel and add the bot as an administrator
with permission to post messages. Add these variables to the Space settings:

- `OWNER_ID`: your numeric Telegram user ID
- `ACCESS_CHANNEL`: the private channel ID, including the `-100` prefix

On its first start, the bot creates and pins a message beginning with
`FSB_ACCESS_STATE_V1`. It updates this message whenever access changes and reads
it again after every restart. Do not edit, delete, or unpin this message. Use a
dedicated channel without another pinned message. The bot needs permissions to
post, edit its own messages, and pin messages.

Available private-chat commands:

- `/id` shows the sender's Telegram user ID and is available before approval
- `/allow <user ID>` grants access (owner only)
- `/deny <user ID>` revokes access (owner only)
- `/users` lists all authorized IDs (owner only)

`ALLOWED_USERS` remains optional and initializes the pinned state when the bot
starts with a new access channel.

## Embedded subtitles

Video link messages include a `Subtitles` button. The bot uses `ffprobe` only
after that button is pressed, lists the embedded tracks, and extracts only the
selected text-based track as SRT. Image-based subtitles such as PGS and VobSub
are intentionally not converted.

The Docker image includes FFmpeg. Extraction streams the source from Telegram;
it does not store the complete video on the Space. The resulting small SRT file
is uploaded to the private log channel and receives a signed link with the same
expiration time as the video.

Optional resource limits (the defaults are suitable for a small Space):

- `SUBTITLE_CONCURRENCY=1`
- `SUBTITLE_PROBE_TIMEOUT_SEC=60`
- `SUBTITLE_EXTRACT_TIMEOUT_SEC=1800`
- `SUBTITLE_MAX_OUTPUT_MB=20`

## Online subtitle search

Add `SUBSOURCE_API_KEY` as a private Space secret to enable the `Search Online`
button on video links. The bot derives a title, year, season, and episode from
the Telegram filename, asks the user to confirm the Subsource match, and shows
ten subtitles per page. Downloads are size-limited, safely unpacked, uploaded
to the private log channel, and served through the existing signed-link flow.

Each user chooses a subtitle language once. The preference is stored inside the
existing pinned `FSB_ACCESS_STATE_V1` message in the private access channel and
is reused after Space restarts. It can be changed from every online-search menu.
