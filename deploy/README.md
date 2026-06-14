# Deploying the assistant as a service

Run the realtime assistant unattended on an always-on machine (an appliance, a
mini-PC, a server with a USB speakerphone), started automatically at boot via
systemd, with its endpoints/secrets in a config file kept out of git.

This pairs with the primary/fallback failover built into the client: point the
**primary** at your preferred realtime endpoint and the **fallback** at a local
LocalAI, and the assistant rides through outages on its own.

## Quick start (user-level, recommended)

```bash
# 1. Install: builds the binary, installs a per-user unit, seeds the config.
deploy/install.sh --user

# 2. Fill in your endpoints + token (created chmod 600, never committed):
$EDITOR ~/.config/realtime-assistant/assistant.env

# 3. Start now and on every boot:
systemctl --user start realtime-assistant
journalctl --user -u realtime-assistant -f
```

`install.sh --user` also runs `loginctl enable-linger` so the service starts at
boot without you logging in. Re-running `install.sh` is safe: it rebuilds and
updates the binary/unit but never overwrites an existing config.

## System-level (root)

```bash
sudo true                       # so the build step can reuse the credential
deploy/install.sh --system --now
sudoedit /etc/realtime-assistant/assistant.env
sudo systemctl restart realtime-assistant
sudo journalctl -u realtime-assistant -f
```

The system unit runs as the invoking user (`User=`) with `SupplementaryGroups=audio`
so it can reach `/dev/snd`.

## Uninstall

```bash
deploy/uninstall.sh --user            # remove the unit, keep config + binary
deploy/uninstall.sh --user --purge    # also delete config + binary
# (use --system for the root-level install)
```

## Configuration

All settings live in `assistant.env` (see
[`realtime-assistant.env.example`](./realtime-assistant.env.example) for the full,
commented list). They are the assistant's native environment variables; the unit
loads them with `EnvironmentFile=` and runs the binary with no flags. The most
important:

| Variable | Meaning |
|---|---|
| `OPENAI_WS_BASE_URL` / `ASSISTANT_MODEL` / `OPENAI_API_KEY` | primary endpoint |
| `FALLBACK_WS_BASE_URL` / `FALLBACK_MODEL` / `FALLBACK_API_KEY` | fallback endpoint |
| `ASSISTANT_LANGUAGE` | input language (ISO-639-1, e.g. `it`) |

## Audio device selection

The audio layer opens ALSA's **default** device — it has no device-selection
flag yet (see the follow-up note below). On a host with several sound cards,
point the default at the one you want via `~/.asoundrc` (user) or
`/etc/asound.conf` (system). For a USB speakerphone reported by `aplay -l` /
`arecord -l` as `card N: USB [...]`:

```
pcm.!default { type plug; slave.pcm "hw:USB,0" }
ctl.!default { type hw; card "USB" }
```

`plug` lets ALSA convert the sample rate/format the assistant requests. Verify
with `speaker-test -D default -c 1` and `arecord -D default -f S16_LE -r 24000 -c 1 test.wav`.

> **Follow-up:** a real `-audio-device` flag on the binary would remove the need
> for ALSA routing. Tracked separately; the routing above is the current
> workaround.

## What gets installed

| | user-level | system-level |
|---|---|---|
| binary | `~/.local/bin/realtime-assistant` | `/usr/local/bin/realtime-assistant` |
| config | `~/.config/realtime-assistant/assistant.env` | `/etc/realtime-assistant/assistant.env` |
| unit | `~/.config/systemd/user/realtime-assistant.service` | `/etc/systemd/system/realtime-assistant.service` |

Building requires Go, a C toolchain, and ALSA headers (`libasound2-dev` /
`alsa-lib-devel`). Use `--no-build` with a prebuilt `./assistant`, or `--bin PATH`.
