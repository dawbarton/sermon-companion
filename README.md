# Sermon Companion

Sermon Companion is a small local application for recording and preparing the
Bible reading, sermon, and questions from a church service. It continuously
captures lossless audio, while the operator marks the useful parts from a dock
inside OBS. After the service, the marker positions can be corrected before the
application normalises each part separately and creates an MP3.

The application is a prototype intended for macOS development and subsequent
validation on the church's Windows and HDMI-capture hardware.

## Why this design

- The complete service is retained as a lossless FLAC recording, so a late or
  mistaken button press does not lose audio.
- Live audio is captured through miniaudio rather than FFmpeg's device
  demuxers. A bounded PCM queue feeds FFmpeg only as a FLAC encoder, avoiding
  the AVFoundation timestamp and capture artefacts observed during development.
- Segment and marker positions come from the accepted audio-frame count, not
  elapsed wall time, so device-clock drift does not accumulate in the edits.
- `Reading`, `Sermon`, and `Q&A` are merely default presets. The stored segment
  model accepts arbitrary `kind` and `label` values.
- Each service stores an editable title and church. New services inherit the
  church from `config.json`; the review interface exposes only segment labels,
  while retaining automatically generated kinds in the generic backend model.
- Every metadata change is appended to `events.jsonl`. `session.json` is an
  atomic current snapshot, not the only copy of the editing history.
- The review page caches a compact waveform envelope for long services. It can
  zoom and pan without loading the complete decoded recording into the browser;
  segment bodies and edges are draggable, every segment has bounded playback,
  and segments can be added, removed, or restored after the service.
- Segment boundaries may touch but cannot overlap. Waveform drags stop at the
  current neighbouring segments rather than jumping over or reordering them;
  the same rule is enforced for typed times and exports.
- FFmpeg and FFprobe are the only runtime dependencies outside the single
  application binary. The former receives raw PCM for FLAC encoding and remains
  responsible for file-based mastering and MP3 export.
- The lossless source is never modified. MP3s and intermediate normalised
  segments are derived outputs.

Miniaudio documents its [cross-platform capture API](https://miniaud.io/docs/manual/),
and FFmpeg's [`loudnorm` filter](https://ffmpeg.org/ffmpeg-filters.html#loudnorm)
implements EBU R128 loudness normalisation. The user interface is an ordinary
local page, compatible with OBS's Chromium-based [browser support](https://obsproject.com/kb/browser-source).

## Try it on macOS

Requirements: Go 1.23 or later, a working C compiler for cgo, FFmpeg, and
FFprobe on `PATH`.

```sh
make test
make run-demo
```

The demo records a real-time synthetic tone rather than an audio device. Open:

- review and export: <http://127.0.0.1:8765/>
- compact OBS dock: <http://127.0.0.1:8765/dock>

Use the dock to start a recording, mark several segments, and stop. In the
review page, click a segment's Play button to audition it, or zoom and pan the
waveform and drag its body or edges to adjust its times. The segment manager can
also add a missed interval or remove an unwanted one. Then create the MP3. Demo
data is written beneath `work/demo-data` and is ignored by Git.

The MP3 is named `YYYY-MM-DD-Church-Name.mp3` using the service date and the
edited church field. The review page can open the containing folder directly.

For capture from a macOS input, omit `--demo`. On first launch the application
creates its configuration in `~/Library/Application Support/Sermon Companion`.
List miniaudio devices and their stable identifiers with:

```sh
go run ./cmd/sermon-companion --list-devices
```

## Windows

See [docs/WINDOWS-SETUP.md](docs/WINDOWS-SETUP.md) for deployment and the OBS
dock setup. Each tagged GitHub release contains a native Windows x86-64
executable, an application ZIP, and SHA-256 checksums. The automatic ZIP does
not redistribute FFmpeg; place `ffmpeg.exe` and `ffprobe.exe` beside the
application, as described in the setup notes.

A self-contained x86-64 folder can also be built on macOS, Linux, or Windows
with:

```powershell
pwsh -File scripts/build-windows.ps1 -FFmpegDirectory C:\path\to\ffmpeg\bin -CCompiler gcc
```

The output in `dist/SermonCompanion` contains the application, start shortcut,
operator notes, and the supplied FFmpeg executables. No Go installation is
required on the church computer.

## Versions and releases

Sermon Companion uses [Semantic Versioning](https://semver.org/). The current
version is stored in `VERSION`. Until 1.0, increment the minor version for new
functionality or an intentionally incompatible session/API change, and the
patch version for compatible corrections. Pre-release suffixes such as
`0.2.0-rc.1` are permitted.

To publish a release, commit the intended `VERSION` value and push an annotated
tag with the same value prefixed by `v`, for example `v0.1.0`. The Windows
release workflow rejects a mismatched or malformed tag, tests the cgo-enabled
native capture build on Windows, embeds the version in the executable, and
creates the GitHub release. Release binaries report their version with
`SermonCompanion.exe --version`; untagged development builds report `dev`.

## Repository map

```text
cmd/sermon-companion/   application entry point
internal/app/           local HTTP API and embedded browser UI
internal/capture/       miniaudio source, audio-frame clock, buffering, and fallback
internal/master/        per-segment two-pass loudness normalisation and MP3
internal/store/         versioned session model and append-only event journal
docs/                   deployment and architecture notes
scripts/                Windows packaging and launch helpers
```

## Current prototype boundary

The buffering, recovery, editing, and mastering paths are automated-tested; the
native capture path also requires a normal host process with microphone/audio
permission, which the Codex sandbox cannot provide. Before routine use, the
exact miniaudio HDMI device identifier, access by OBS and Sermon Companion at
the same time, channel layout, and a complete service-length recording must be
validated on the church Windows computer. Some device drivers allow only one
application to open a device. If the HDMI device is exclusive, the planned
fallback is a thin OBS raw-mix adapter feeding the same PCM recorder and sample
clock, rather than a second device capture.
