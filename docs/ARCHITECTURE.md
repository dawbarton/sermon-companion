# Architecture and data format

## Process boundary

```text
HDMI audio device                         OBS custom browser dock
        |                                           |
        v                                           v
miniaudio callback --------> audio-frame clock <---- local HTTP server
        |                         |
        v                         v
bounded PCM queue             session.json + events.jsonl
        |
        v
FFmpeg raw-PCM FLAC encoder ---> audio.part.flac
        |                                  |
        +--------------------------> review and marker adjustment
                                             |
                                             v
                                two-pass loudnorm per segment
                                             |
                                             v
                                        final MP3
```

Sermon Companion links miniaudio through the pinned `malgo` Go bindings. The
primary source uses CoreAudio on macOS and shared-mode WASAPI on Windows. Its
callback copies signed 16-bit PCM into a preallocated, bounded queue; a separate
goroutine feeds FFmpeg's raw PCM input and produces FLAC. The callback never
performs disk or process I/O.

| Backend | Use | Timing |
| --- | --- | --- |
| `miniaudio` | Primary macOS and Windows device capture | Accepted audio frames |
| `ffmpeg` + `lavfi` | Deterministic demo and integration tests | Reported encoded position |
| `ffmpeg` + device/custom driver | Explicit diagnostic fallback | Reported encoded position |

The default recording format is stereo, 48 kHz, signed 16-bit PCM encoded to
FLAC. A future OBS raw-mix source can feed the same queue and frame clock without
altering session storage, the UI, or mastering. This is the preferred fallback
if OBS and the companion cannot share the HDMI device.

## Audio clock and buffering

The audio callback records cumulative accepted frames alongside monotonic local
times. When the operator starts or stops a segment, the server waits briefly for
the callback following the button event and interpolates between the surrounding
frame anchors. The stored frame number is canonical; seconds are derived for the
browser. Device-clock drift therefore cannot accumulate between a marker and the
recorded file.

The fallback FFmpeg backend has no callback, so it anchors the same clock to the
audio position FFmpeg reports through `-progress` every 200 ms. Device start-up
latency and encoder pacing therefore stay out of the marker positions; an
elapsed wall-clock estimate placed every mark about half a second ahead of the
matching audio. This needs FFmpeg 4.4 or later for `-stats_period`.

The PCM queue defaults to ten seconds. If it fills, capture stops and the session
is marked failed rather than silently dropping samples. A completed capture is
published only when all accepted frames were written to the encoder. Session
metadata records accepted and written frames, dropped frames, callback count,
queue high-water level, wall and audio durations, and their drift in parts per
million.

## Generic segment model

A session stores the service title and church alongside its recording state.
Both fields remain editable after capture and are included in the append-only
metadata history. A new session's church defaults from the top-level `church`
setting in `config.json`.

A segment has a stable ID, free-form `kind`, user-facing `label`, canonical start
and end frames, derived times in seconds, an `include` flag, and an optional
`archived` flag. Removing a
segment archives it from the active editor and mastering pipeline; restoring it
clears that flag. The event journal retains both operations. The three dock
presets are configuration:

```json
[
  {"kind": "reading", "label": "Reading"},
  {"kind": "sermon", "label": "Sermon"},
  {"kind": "questions", "label": "Q&A"}
]
```

They have no special meaning to the storage or mastering layers. A marker is
likewise generic, but represents a point rather than an interval.

The ordinary review UI exposes only labels. For a manually added segment or
marker, the server derives a lower-case, hyphenated kind from its label. The API
retains an optional explicit kind for future integrations.

## Durability

Each session is an independent directory:

```text
sessions/SESSION-ID/
  audio.flac                 immutable source after a clean stop
  capture.log
  events.jsonl               append-only metadata journal
  session.json               atomically replaced current snapshot
  waveform-20pps.json        cached compact peak envelope
  exports/
    mastering.log
    YYYY-MM-DD-Church-Name.mp3
    previous/                  superseded exports retained here
```

An event is appended and flushed before the new snapshot replaces the previous
snapshot. Adjusting a segment records its previous value and the requested
change. Exports are first written under a private work directory and renamed
only after FFmpeg succeeds. Successful intermediate files are then removed;
failed intermediates and the mastering log remain for diagnosis.

A lossless recording is roughly 500 MB for a service, so the application applies
a retention period at start-up: a session directory is deleted outright once the
service is older than `retentionDays`, defaulting to 60. This takes the exports
with it. The MP3 is produced for the church website and the copy of record lives
there, so nothing on this machine is intended to be permanent. A deletion is
logged when it happens, because the session's own journal goes with it. Setting
`retentionDays` to zero keeps every service indefinitely.

The current MP3 name is deterministic from the local service date and a
filesystem-safe, space-free form of the session's church. Re-exporting first
moves the preceding file into `exports/previous`, then atomically publishes the
new current file. The manager asks the operating system to reveal `exports`
when the operator clicks "Open MP3 folder".

## Mastering

Included, complete segments are sorted chronologically. FFmpeg trims each one by
its exact start and end sample and, by default, folds it to a single channel
before it is processed with
the FFmpeg `loudnorm` filter in two passes using the configured integrated
loudness, loudness range, and true-peak targets. The second pass renders a
normalised FLAC. These homogeneous files are concatenated and encoded once with
LAME. The defaults are `-19 LUFS`, `11 LU`, `-1.5 dBTP`, and LAME variable
bitrate at quality 5.

The downmix precedes the loudness measurement in both passes. Two identical
channels measure about 3 LU louder than the one channel they carry, so
normalising the stereo pair and folding afterwards would deliver a file below
the target. A service is one speaker through one mix, so the fold costs nothing
audible, and the -19 LUFS target assumes a mono file: Apple's spoken-word
recommendation is -16 LUFS in stereo and -19 LUFS in mono. The saving in bytes
is not the reason, and is small; LAME's joint stereo already spends almost
nothing on a side channel that is silent. `mastering.mono` turns the fold off
for a genuinely stereo source. The lossless recording is untouched.

`loudnorm` aims at a true peak but does not guarantee one once its 192 kHz
working audio is resampled, so `alimiter` holds the rendered segment below a
configured ceiling, `-1 dBFS` by default, with its own auto-levelling disabled
so that it cannot undo the loudness normalisation. Silence is appended to every
segment but the last, which spaces the parts in the MP3 without a pause before
the first or after the last. It is added after the loudness pass, so a gap
cannot pull the measured level of the speech about, and the length comes from
the service where the operator has set one and from `mastering.gapSeconds`
otherwise. Both are export-only: the recording and the reviewed times are
unchanged. A service is speech with pauses, so a variable bitrate
spends far less than a constant 128 kbit/s on it without sounding worse.

There is deliberately no crossfade: the exported boundary should match the
operator's reviewed times. A future UI may offer short, explicit fades to avoid
clicks at non-zero crossings.

## Waveform and review interaction

FFmpeg decodes the source to mono, 8 kHz signed PCM once. The backend retains
the maximum absolute amplitude for each 50 ms interval and stores it as one
unsigned byte, base64-encoded in the cache. At 20 points per second, a 75-minute
service has 90,000 peaks. The browser draws only the peaks in the current view,
so zooming and panning do not require the full decoded audio in memory.

The overview spans the complete service. The view can be zoomed to ten seconds
and panned chronologically. Clicking seeks the lossless audio; dragging a block
moves a segment without changing its duration; dragging an edge changes only
the corresponding timestamp. A drag is written through the same audited
`segment.adjusted` API as the text fields. Per-segment playback seeks to the
start and pauses automatically at the end.

Segments preserve their chronological order during waveform editing. A whole
segment is confined to the free interval between its current neighbours, while
each edge stops at the adjacent boundary. Touching endpoints are valid, but
overlap is rejected by the API for additions, adjustments, and restoration, and
is checked again before mastering. Segments do not jump across one another; an
explicit reorder operation can be added later if a genuine use case emerges.

Manual additions use the same generic model and appear immediately on the
waveform. Removal is deliberately reversible: the current snapshot marks a
segment as archived, while `events.jsonl` retains its complete prior value.
Adding, editing, removing, or restoring a segment marks any earlier MP3 as
stale, so the interface prompts for a new export rather than presenting an old
file as current.

## Local API

The UI uses a small JSON API beneath `/api`. It supports session start and stop,
generic segment start, stop, and adjustment, point markers, session history,
lossless playback, waveform-envelope generation, and asynchronous export. It can
also ask the operating system to open the MP3 folder or the review page, because
the dock runs inside OBS's embedded browser where an ordinary link would open in
the dock panel itself. The server listens on loopback by
default and sets a restrictive content-security policy. It has no cloud or OBS
WebSocket dependency. Live status includes the audio-frame position and capture
health statistics.

## Repository map

```text
cmd/sermon-companion/   application entry point
internal/app/           local HTTP API and embedded browser UI
internal/capture/       miniaudio source, audio-frame clock, buffering, and fallback
internal/config/        configuration defaults and create-on-first-run behaviour
internal/master/        per-segment two-pass loudness normalisation and MP3
internal/store/         versioned session model and append-only event journal
internal/waveform/      cached compact peak envelope
docs/                   deployment and architecture notes
scripts/                Windows packaging and launch helpers
```

## Validation boundary

The buffering, recovery, editing, retention, and mastering paths have automated
tests. The native capture path additionally needs a normal host process with
audio permission, which a sandbox cannot provide. Before routine use, confirm on
the church Windows computer: the exact miniaudio HDMI device identifier, access
by OBS and Sermon Companion at the same time, the channel layout, and one
complete service-length recording. Some drivers allow only one application to
open a device. If the HDMI device turns out to be exclusive, the intended
fallback is a thin OBS raw-mix adapter feeding the same PCM recorder and sample
clock, rather than a second device capture.
