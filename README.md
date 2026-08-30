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
- `Reading`, `Sermon`, and `Q&A` are merely default presets. The stored segment
  model accepts arbitrary `kind` and `label` values.
- Every metadata change is appended to `events.jsonl`. `session.json` is an
  atomic current snapshot, not the only copy of the editing history.
- The review page caches a compact waveform envelope for long services. It can
  zoom and pan without loading the complete decoded recording into the browser;
  segment bodies and edges are draggable, every segment has bounded playback,
  and segments can be added, removed, or restored after the service.
- FFmpeg is the only runtime dependency outside the single application binary.
  Its input adapter is selected in `config.json`: DirectShow on Windows,
  AVFoundation on macOS, or a custom argument list.
- The lossless source is never modified. MP3s and intermediate normalised
  segments are derived outputs.

FFmpeg documents [DirectShow and AVFoundation capture](https://ffmpeg.org/ffmpeg-devices.html),
and its [`loudnorm` filter](https://ffmpeg.org/ffmpeg-filters.html#loudnorm)
implements EBU R128 loudness normalisation. The user interface is an ordinary
local page, compatible with OBS's Chromium-based [browser support](https://obsproject.com/kb/browser-source).

## Try it on macOS

Requirements: Go 1.23 or later, FFmpeg, and FFprobe on `PATH`.

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

For capture from a macOS input, omit `--demo`. On first launch the application
creates its configuration in `~/Library/Application Support/Sermon Companion`.
List AVFoundation devices with:

```sh
go run ./cmd/sermon-companion --list-devices
```

## Windows

See [docs/WINDOWS-SETUP.md](docs/WINDOWS-SETUP.md) for deployment and the OBS
dock setup. A distributable x86-64 folder can be built on macOS, Linux, or
Windows with:

```powershell
pwsh -File scripts/build-windows.ps1 -FFmpegDirectory C:\path\to\ffmpeg\bin
```

The output in `dist/SermonCompanion` contains the application, start shortcut,
operator notes, and the supplied FFmpeg executables. No Go installation is
required on the church computer.

## Repository map

```text
cmd/sermon-companion/   application entry point
internal/app/           local HTTP API and embedded browser UI
internal/capture/       replaceable FFmpeg capture adapters and lifecycle
internal/master/        per-segment two-pass loudness normalisation and MP3
internal/store/         versioned session model and append-only event journal
docs/                   deployment and architecture notes
scripts/                Windows packaging and launch helpers
```

## Current prototype boundary

The capture, recovery, editing, and mastering paths are implemented and tested
with a synthetic source. Before routine use, the exact HDMI DirectShow device
name, access by OBS and FFmpeg at the same time, channel layout, and a complete
service-length recording must be validated on the church Windows computer.
Some device drivers allow only one application to open a device. If the HDMI
device is exclusive, the next adapter should capture a dedicated OBS audio
monitoring device or an OBS-recorded lossless audio track instead.
