# Voice: speech-to-text and text-to-speech

Nimbus-One talks and listens on Telegram and the web console, and in the
terminal. See [User Guide](User-Guide.md) for setup flows and
[Agent Guide](Agent-Guide.md) for the `transcribe` / `speak` tool contracts.

## What works where

| Surface | Speak (TTS) | Listen (STT) |
|---|---|---|
| Terminal | `nimbus-one speak "hi"` (termux-tts-speak / say / espeak) | `nimbus-one transcribe note.ogg` |
| Telegram | `/speak <text>` voice reply | voice notes auto-transcribed into chat |
| Web console | 🔊 button per reply (browser speech) | 🎤 button (browser mic → server STT) |
| Agent tools | `speak` (play or file) | `transcribe` (audio path → text) |
| HTTP API | `POST /api/v1/speak` → audio bytes | `POST /api/v1/transcribe` (multipart `audio`) |

## Engines (local-first, API second)

- **STT chain:** explicit `NIMBUS_STT_URL` → local whisper.cpp binary +
  ggml model (`NIMBUS_WHISPER_MODEL`, auto-found in `./models/`) →
  OpenAI-compatible `/audio/transcriptions` (OpenAI key) → exact setup
  steps, never silence. `.ogg`/Opus uploads travel as-is — no ffmpeg.
- **TTS chain:** `espeak-ng --stdout` / macOS `say -o` files first, then
  OpenAI-compatible `/audio/speech` (mp3). Instant playback uses
  termux-tts-speak / say / espeak directly.

## Notes

- Telegram voice notes download via `getFile`, transcribe, and enter the
  conversation prefixed `[voice 0:12]` — history, memory, and skills all
  see the transcript like any message.
- Transcription failures reply in-chat with the concrete cause and fix
  (`nimbus-one fix` topic IDs apply); nothing is dropped silently.
- Browser mic needs HTTPS or localhost (browser policy, not Nimbus-One).
  Server-side STT still needs one configured backend — see table above.
