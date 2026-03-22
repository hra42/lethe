# lethe

<p align="center">
  <img src="lethe.jpg" alt="lethe" width="256">
</p>

Auto-deletes Discord messages older than a configurable threshold from one or more channels.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DISCORD_TOKEN` | yes | — | Bot token |
| `CHANNEL_IDS` | yes | — | Comma-separated channel IDs |
| `MAX_AGE` | yes | — | Message age threshold (e.g. `720h`, `168h`) |
| `INTERVAL` | no | `6h` | How often to run cleanup |

## Quick Start

### Docker Compose

```bash
cp .env.example .env
# edit .env with your values
docker compose up -d
```

### Without Docker

```bash
go build -o lethe .
DISCORD_TOKEN=... CHANNEL_IDS=123,456 MAX_AGE=720h ./lethe
```

## Discord Bot Setup

1. Create an application at https://discord.com/developers/applications
2. Go to **Bot** → copy the token → set as `DISCORD_TOKEN`
3. Go to **OAuth2** → **URL Generator** → select scope `bot`
4. Select permissions: **Manage Messages**, **Read Message History**
5. Open the generated URL to invite the bot to your server

## How It Works

- On start, immediately deletes all messages older than `MAX_AGE`
- Then repeats every `INTERVAL`
- Messages < 14 days old are bulk-deleted (up to 100 per API call)
- Messages >= 14 days old are deleted individually (Discord API limitation)
- Supports multiple channels via comma-separated `CHANNEL_IDS`
- Shows as online in Discord with "Watching cleanup" status

## License

[Unlicense](LICENSE)
