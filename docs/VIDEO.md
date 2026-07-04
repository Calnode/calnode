# Calnode Rooms — video setup guide

Calnode Rooms is Calnode's built-in video meeting feature: a LiveKit-backed, in-browser
meeting room usable as a booking location, the same way you'd use Zoom or Google Meet —
except it's infrastructure you control. Guests join with a link; no app install or
account required.

It's **BYO-LiveKit**: Calnode doesn't run or bundle a LiveKit server. You point Calnode
at a LiveKit project (Cloud or self-hosted), and Calnode handles token minting, the room
UI, recording, consent, and the AI notetaker on top. This doc covers the Calnode-specific
config — LiveKit's own docs cover standing up LiveKit itself.

For architecture-level detail (token model, host authority, egress lifecycle), see
`docs/ARCHITECTURE.md` §22.

## 1. Connect your LiveKit

Go to **Settings → Video** and fill in three fields:

| Field | Example |
|---|---|
| **Server URL** | `wss://yourproject.livekit.cloud` |
| **API Key** | `APIxxxxxxxx` |
| **API Secret** | (stored encrypted — shows a placeholder once saved, never re-displayed) |

These map directly to `PATCH /v1/settings/livekit`: `{"url": "...", "api_key": "...", "api_secret": "..."}`.
Once set, **"Calnode Video (LiveKit)"** becomes a selectable location on any event type.

### Option A — LiveKit Cloud (fastest)

1. Create a project at [cloud.livekit.io](https://cloud.livekit.io).
2. Copy the **WebSocket URL**, **API Key**, and **API Secret** from the project's Settings tab.
3. Paste them into Calnode's **Settings → Video**.

### Option B — self-hosted LiveKit

Follow LiveKit's own deployment guide to stand up your server:
[docs.livekit.io/home/self-hosting/deployment](https://docs.livekit.io/home/self-hosting/deployment/).
Calnode doesn't need anything LiveKit-specific beyond a reachable WebSocket URL and an
API key/secret pair — once your server is running, point Calnode at it exactly the same
way as the Cloud path above.

## 2. Recording → your bucket

Recording deliberately reuses your existing Litestream backup bucket rather than
requiring separate storage setup:

- If `LITESTREAM_REPLICA_URL`, `LITESTREAM_ACCESS_KEY_ID`, and `LITESTREAM_SECRET_ACCESS_KEY`
  are set (see `DEPLOY.md` §6), recordings become storage-ready automatically.
  `LITESTREAM_REGION` and `LITESTREAM_ENDPOINT` are optional, for non-AWS S3-compatible
  providers.
- Turn recording on in **Settings → Storage** — the "Allow hosts to record meetings"
  toggle (`recordings_enabled`). It stays disabled until storage is ready.
- Files land under a fixed prefix: `recordings/{room}/{UTC timestamp}.mp4` — e.g.
  `recordings/booking-abc123/20260704T143022Z.mp4`.
- Finished recordings are listed on the **Recordings** page with presigned, short-lived
  download links — nothing is public.
- (Optional, recommended) register the webhook: copy the URL shown in **Settings →
  Video** (`{BASE_URL}/v1/livekit/webhook`) into LiveKit Cloud → Project → Settings →
  Webhooks, signed with the same API key. Recording still finalizes without it — the
  webhook just adds accurate duration and a clean "room closed" backstop.

## 3. Recording consent

When a host starts recording, everyone in the room gets a notice: an audio announcement,
plus a modal —

> **This meeting is being recorded**
> By continuing you consent to being recorded. If you don't consent, you can leave the
> meeting.
> [Continue] [Leave]

Be precise about what this is: **recording is never blocked or gated by consent.** It
starts the moment the host clicks Record, the same as Zoom/Meet/Teams. "Continue"/"Leave"
is a notice-and-audit mechanism, not an enforcement gate — clicking "Leave" disconnects
that participant, but the recording keeps running for everyone else. Every
acknowledgment (continue or leave) is logged with a timestamp, one row per participant
per room, for accountability. The host's own click to start recording counts as their
consent — they never see the modal.

## 4. AI notetaker

Three prerequisites, all required:

1. Recording enabled (§2).
2. A Deepgram API key entered in **Settings → Video** (`stt_api_key`).
3. An LLM configured (**Settings → AI**) — Calnode never ships its own model.

Be honest about what actually happens — this is a mixed self-hosted/third-party pipeline:

- **Transcription is not self-hosted.** The finished recording is never downloaded by
  Calnode — it hands Deepgram a short-lived presigned URL to the file directly, so the
  audio does leave your server and go to Deepgram's API.
- **Summarization uses your own LLM.** Once the transcript comes back, Calnode sends it
  to whichever LLM endpoint you configured (BYO-LLM) to generate the meeting notes —
  that part stays on infrastructure you control.
- **It's post-meeting, not live.** Transcription and summarization both run
  asynchronously after the recording finishes, as background jobs — there's no live
  captioning or in-call transcript.
- **Speakers are labelled generically.** Diarization gives you "Speaker 0", "Speaker 1",
  etc. — not real names.

## 5. Host controls

Available to whoever currently holds host in the room (the durable host — the booking
owner, or whoever holds the host room token — or a participant the durable host has
handed it to):

- **End meeting for everyone** — closes the room for all participants immediately.
- **Hand off host** — make another participant the host.
- **Reclaim host** — the durable host can take host back at any time, even after
  handing it off.
- **Toggle attendee screen-share** — on by default for the host, off by default for
  attendees; the host can flip it on/off for the room.

There's no remote mute for other participants today — each person controls only their
own mic.

## 6. Headless / agents

Meetings are consumable programmatically, not just through the room UI.

**MCP tools** (role-scoped: members only see bookings they host; admins/the owner see
everything):

- `get_meeting_notes(booking_id)` — the AI-generated Markdown notes for a booking's meeting.
- `get_transcript(booking_id)` — the raw diarized transcript ("Speaker 0", "Speaker 1", …).

**Webhooks** (register in **Settings → Webhooks**; booking-shaped payload, HMAC-SHA256
signed):

- `recording.completed` — a meeting's recording has finished and is in your bucket.
- `transcript.ready` — Deepgram transcription is done.
- `notes.ready` — the LLM summary is done.

All three fire with the booking's id — fetch the actual transcript/notes content via the
REST API or the MCP tools above.
