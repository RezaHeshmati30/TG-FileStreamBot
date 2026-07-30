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
