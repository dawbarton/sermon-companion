# Architecture and data format

## Process boundary

```text
HDMI audio device                         OBS custom browser dock
        |                                           |
        v                                           v
FFmpeg capture process <---- Sermon Companion local HTTP server
        |                         |                 |
        v                         v                 v
audio.part.flac          session.json       events.jsonl
        |                         |
        +-------------> review and marker adjustment
                                  |
                                  v
                     two-pass loudnorm per segment
                                  |
                                  v
                             final MP3
```

Sermon Companion does not link to an audio library. `internal/capture` builds an
FFmpeg command from a small adapter configuration. The shipped adapters are:

| Driver | Development or production use | FFmpeg input |
| --- | --- | --- |
| `dshow` | Windows HDMI capture | `-f dshow -i audio=DEVICE` |
| `avfoundation` | macOS input-device prototype | `-f avfoundation -i :DEVICE` |
| `lavfi` | deterministic synthetic test | `-f lavfi -i FILTER` |
| `custom` | future platform or routing adapter | explicit `inputArgs` array |

All adapters produce the same stereo, 48 kHz FLAC source. A native Windows
capture implementation can therefore replace only `internal/capture`, without
altering session storage, the UI, or mastering.

## Generic segment model

A segment has a stable ID, free-form `kind`, user-facing `label`, start and end
times in seconds, an `include` flag, and an optional `archived` flag. Removing a
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
    sermon.mp3
```

An event is appended and flushed before the new snapshot replaces the previous
snapshot. Adjusting a segment records its previous value and the requested
change. Exports are first written under a private work directory and renamed
only after FFmpeg succeeds. Successful intermediate files are then removed;
failed intermediates and the mastering log remain for diagnosis.

## Mastering

Included, complete segments are sorted chronologically. Each is processed with
the FFmpeg `loudnorm` filter in two passes using the configured integrated
loudness, loudness range, and true-peak targets. The second pass renders a
normalised FLAC. These homogeneous files are concatenated and encoded once with
LAME. The defaults are `-16 LUFS`, `11 LU`, `-1.5 dBTP`, and 128 kbit/s MP3.

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

Manual additions use the same generic model and appear immediately on the
waveform. Removal is deliberately reversible: the current snapshot marks a
segment as archived, while `events.jsonl` retains its complete prior value.
Adding, editing, removing, or restoring a segment marks any earlier MP3 as
stale, so the interface prompts for a new export rather than presenting an old
file as current.

## Local API

The UI uses a small JSON API beneath `/api`. It supports session start and stop,
generic segment start, stop, and adjustment, point markers, session history,
lossless playback, waveform-envelope generation, and asynchronous export. The server listens on loopback by
default and sets a restrictive content-security policy. It has no cloud or OBS
WebSocket dependency.
