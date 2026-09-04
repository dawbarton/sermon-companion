# Sermon Companion: guidance for future agents

## Project purpose and status

Sermon Companion is a maintainable, standalone companion for OBS. It continuously
records a church service as lossless audio, lets an operator mark useful
segments in an OBS Custom Browser Dock, provides post-service waveform editing
and auditioning, levels the included segments together by label, and produces a
single MP3.

Production deployment targets Windows and an HDMI capture-audio device. The
primary backend uses miniaudio through `malgo`; synthetic `lavfi` input remains
available for deterministic development. The precise miniaudio device ID,
simultaneous access by OBS and the companion, channel layout, and a complete
service-length recording still require validation on the church computer. If
the device is exclusive, add an OBS raw-mix source feeding the existing PCM
queue rather than redesigning the session, editing, or mastering layers.

Read `README.md`, `docs/ARCHITECTURE.md`, and `docs/WINDOWS-SETUP.md` before
changing user-visible behaviour or deployment.

## Non-negotiable product invariants

- Preserve the complete source recording. Never edit, trim, normalise, or
  overwrite `audio.flac` in place. The configured retention period is the sole
  exception, and it deletes a whole session rather than altering a recording.
- Recording is continuous. Dock actions create generic markers and segments;
  they do not pause the underlying capture.
- Audio-frame positions are canonical for live segments and markers. Never
  replace them with wall-clock elapsed time; seconds are a derived UI value.
  This binds the fallback FFmpeg backend too: it anchors the same frame clock to
  the position FFmpeg reports through `-progress`, because elapsed wall time
  since the process was spawned includes the device start-up latency and placed
  every mark about half a second ahead of the matching audio.
- The native callback must only copy into its preallocated bounded queue and
  advance the frame clock. Never perform disk, process, or metadata I/O there.
- Queue overflow or an accepted/written frame mismatch is a capture failure.
  Never hide missing samples or publish an apparently complete FLAC.
- `Reading`, `Sermon`, and `Q&A` are configurable presets, not special backend
  types. Keep the marker and segment model generic and extensible.
- The ordinary interface exposes the user-facing segment `label`. The backend
  retains `kind` for presets and future integrations; manually created kinds
  are derived from labels unless explicitly supplied through the API.
- Metadata updates must remain non-destructive and auditable. Append and flush
  an event in `events.jsonl` before atomically replacing `session.json`.
- Segment removal is archival and reversible. Do not delete its history.
- Retention deletes an expired session directory outright, exports included.
  A service is roughly 500 MB, and the published MP3's copy of record is the
  church website, not this machine. Log each deletion, because the session's own
  journal goes with it, and never apply retention to an active recording.
- Complete, non-archived segments may touch but must never overlap, including
  segments excluded from export. Enforce this in the browser interaction, the
  HTTP API, and immediately before mastering. Whole-segment dragging is clamped
  between its current neighbours and must not jump over or reorder them.
- Browser times are displayed to tenths of a second. Snap values within 0.051 s
  of a neighbouring boundary before strict overlap validation so an unchanged,
  rounded value remains saveable.
- Any metadata or segment edit makes an existing export stale.
- Master each included segment separately with two-pass FFmpeg `loudnorm`, then
  concatenate the resulting lossless files and encode MP3 once. Do not add
  implicit crossfades.
- Publish exports atomically. Re-exporting retains the preceding MP3 beneath
  `exports/previous`.
- The current MP3 name is `YYYY-MM-DD-Church-Name.mp3`, using the session's
  editable church field normalised to a safe, space-free filename. A new session
  inherits its church from configuration.
- Keep the server loopback-only by default and suitable for OBS's embedded
  Chromium browser. There is deliberately no cloud service or OBS WebSocket
  dependency.
- A capture-device selection is refused unless that device is present, so
  `config.json` can never come to name a device that will fail to open during a
  service. The interface's test for a device that has gone missing must mirror
  the capture backend's own lookup, including its case-insensitive comparison of
  identifiers, or the two will disagree about what will happen on Sunday.
  Starting a recording against a missing device fails; it never falls back to
  another one.
- The application runs without a window or a console. Anything the operator has
  to be told must reach the dock, the review pages, or the log page. Never add
  something whose only output is a console line, and never let a start-up
  failure be silent.
- Closing the application is signalled by the tray's own action and by returning
  from `runTray`, and the signal must be safe to raise more than once. Do not
  make it depend on systray's exit callback: Windows invokes that from its quit,
  but macOS quits by stopping the run loop, where the callback runs only if the
  process is genuinely terminating. Depending on it left the application alive
  with a dead icon.
- Keep the operator workflow understandable to people who know basic OBS but
  are not technical users. Prefer clear controls and recoverable actions over
  exposing implementation detail.
- The dock shares a narrow, short column with other OBS docks. Before recording
  it shows the device list, Start Recording, and Review Recordings, and nothing
  else; the recording controls replace them once a service is under way. Weigh
  every new dock element against the vertical space it costs, and prefer putting
  information on an existing control over adding a line.
- The review page saves as it goes. A field or control there must apply its own
  change rather than waiting for a save button, and must show the value the next
  MP3 would actually use if a change is rejected.

## Architecture and important files

The project is a Go 1.23 module with pinned `malgo`/miniaudio bindings. Native
capture requires cgo and a C compiler at build time. The browser assets are
embedded in the application binary; FFmpeg and FFprobe are the only external
runtime dependencies.

The project uses Semantic Versioning, beginning at `0.1.0`; `VERSION` is the
single source of the release version. While the project remains below 1.0,
increment MINOR for new functionality or intentionally incompatible session/API
changes, and PATCH for compatible corrections. A release tag is `v` followed
by the exact `VERSION` value. Release builds inject that value into the binary;
ordinary development builds identify themselves as `dev` through `--version`.

- `cmd/sermon-companion/main.go`: flags, platform data directory, application
  assembly, local server lifecycle, bundled-FFmpeg discovery, logging set-up,
  and the single-instance check that binding the listening socket performs.
  Alongside it, `tray.go` shows the notification-area icon, `icon.go` draws that
  icon in code, and `console_windows.go` attaches to a command prompt's console
  so that a windowless build can still answer `--version`.
- `internal/config`: configuration defaults, create-on-first-run behaviour, and
  the live settings the dock writes a chosen device back through.
- `internal/applog`: rolling log file and the bounded tail the log page shows.
- `internal/capture`: miniaudio source, bounded PCM buffering, audio-frame clock,
  FLAC encoder lifecycle, device enumeration, and explicit FFmpeg fallback.
- `internal/store`: versioned session model, atomic snapshot persistence,
  append-only event journal, and segment invariants.
- `internal/app`: loopback HTTP API and embedded dock/manager UI.
- `internal/waveform`: cached compact waveform envelope for long recordings.
- `internal/master`: segment extraction, two-pass normalisation measured per
  label so that one talk cut into pieces keeps a single volume, concatenation,
  MP3 publication, and export history.
- `scripts/build-windows.ps1`: distributable Windows folder builder.
- `scripts/Start Sermon Companion.cmd`: non-technical Windows launcher.
- `.github/workflows/release-windows.yml`: tag-driven, cgo-enabled Windows
  testing, packaging, checksums, and GitHub release publication.

Session data belongs outside Git. A session directory contains the immutable
source, capture and mastering logs, event journal, current snapshot, cached
waveform, and exports. The platform defaults are `%LOCALAPPDATA%\Sermon
Companion` on Windows and `~/Library/Application Support/Sermon Companion` on
macOS. Development and browser-test data should go under the ignored `work/`
directory.

The default local pages are:

- Manager: `http://127.0.0.1:8765/`
- OBS dock: `http://127.0.0.1:8765/dock`
- Log: `http://127.0.0.1:8765/log`

The application log sits beside the sessions, under `logs/`.

The principal configuration fields are `listen`, `ffmpeg`, `ffprobe`, `church`,
`capture`, `presets`, and `mastering`. The application creates `config.json` on
first launch and rewrites it when the operator chooses a capture device, so a
change to the file's shape has to survive being written back as well as read. Do not add a hand-maintained sample configuration that can drift
from `internal/config/config.go` without a specific reason and a consistency
test.

## Development and verification

Use the repository's standard commands:

```sh
make test
make run-demo
go test -race ./... -count=1
go vet ./...
node --check internal/app/static/review.js
node --check internal/app/static/dock.js
node --check internal/app/static/log.js
git diff --check
```

`make run-demo` records a real-time synthetic tone under `work/demo-data`. It
requires `ffmpeg` and `ffprobe` on `PATH`. Integration tests that exercise media
processing likewise require those executables; understand any skip rather than
assuming the full path was tested.

For changes affecting browser behaviour, run the demo and inspect both the
manager and dock in a browser. Exercise the user-visible path, check the browser
console, and test a service long enough to cover the changed interaction. For
segment editing, verify edge dragging, whole-segment dragging, typed times,
manual addition, removal/restoration, per-segment playback, zoom/pan, and export
staleness as relevant. Browser automation may not support synthetic pointer
events, so supplement UI inspection with focused JavaScript review and API/store
tests; state any interaction that could not be automated.

Before calling a release build complete, run `scripts/build-windows.ps1` with a
Windows-capable C compiler. A pure-Go cross-build compiles only the no-cgo stub
and is not a usable miniaudio release. Hardware-dependent WASAPI behaviour
cannot be certified on macOS; report this boundary explicitly. The same applies
to the Windows shell: the absence of a console under `-H=windowsgui`, the
notification-area icon, the message box shown for a start-up failure, and the
console attachment that keeps `--version` working are all reasoned from the API
contracts and cannot be verified from macOS. Say so rather than implying they
were tested.

The canonical automated release environment is the pinned `windows-2025`
GitHub runner with MSYS2 UCRT64 GCC. A tag matching `v*.*.*` starts the workflow,
but publication proceeds only if it is valid SemVer and exactly matches
`VERSION`. The workflow runs the Go tests with cgo enabled, builds the executable,
checks its embedded version, creates an application ZIP and SHA-256 manifest,
and creates or updates the corresponding GitHub release. The automatic package
does not redistribute FFmpeg; users add trusted `ffmpeg.exe` and `ffprobe.exe`
files beside the application. The manual PowerShell builder can still bundle a
supplied FFmpeg directory for a local self-contained package. A manual workflow
dispatch performs the same Windows test and build without publishing a release;
use it to validate a release commit before tagging.

## Code and persistence practices

- Keep dependencies minimal. Prefer the Go standard library and native browser
  APIs unless a dependency has a clear maintenance benefit. The exceptions are
  `malgo` for miniaudio and `fyne.io/systray` for the notification-area icon,
  which the standard library cannot provide and which the application needs
  because it has no window of its own. Draw or generate assets in code rather
  than committing binary files for them.
- Keep platform-specific capture concerns behind `internal/capture`. Do not let
  CoreAudio or WASAPI assumptions leak into the store, API, UI, waveform, or
  mastering packages.
- Validate data at every trust boundary. UI constraints are usability features,
  not substitutes for API and mastering validation.
- Use atomic writes and explicit error handling for recordings, metadata, and
  exports. A power loss or forced restart must leave recoverable source audio
  and intelligible session state.
- Add focused unit tests for pure rules and the PCM queue, HTTP tests for API
  behaviour, and FFmpeg-backed integration tests for encoding, waveform, and
  mastering paths. Real miniaudio device tests require a normal host process
  with audio permission and cannot be claimed from a sandbox exposing only a
  null device.
- Use British English in UI text and documentation.
- Keep `README.md`, architecture notes, and Windows operator instructions in
  step with user-visible or deployment changes.

## Git and deliverables

- Commit each substantive, verified step with a message explaining what changed
  and why. Preserve unrelated user changes and never override `.gitignore`.
- Do not commit recordings, generated waveforms, session data, binaries,
  packaged distributions, or other reproducible build artefacts. `work/`,
  `sessions/`, `dist/`, and `outputs/` are intentionally ignored.
- User-facing deliverables for this Codex workspace belong in `outputs/`, even
  though that directory is ignored. Rebuild local prototypes, source archives,
  and setup text after changes that affect them, and link only those local
  deliverables in the final response. Tagged Windows binaries are built and
  retained by GitHub Actions rather than committed or copied back into Git.
- A source archive must contain committed source, not local recordings,
  temporary test data, or Git metadata. Confirm the working tree is clean and
  report the commit identifier used for the build.
- For a release, update and commit `VERSION`, create an annotated `vVERSION`
  tag on that commit, and push the branch before the tag. Never move or reuse a
  published version tag; make a new patch release instead.

## Current design choices, not accidental limitations

- Segments preserve chronological order and cannot jump across neighbours.
- Excluded, non-archived segments still reserve their interval, preventing an
  overlap if they are later re-enabled.
- Archived segments do not constrain the active timeline, but restoration is
  validated against current segments.
- The compact waveform cache uses 20 peaks per second and the browser renders
  only the visible range, supporting roughly 75-minute services with zoom and
  pan.
- The default lossless capture format is stereo, 48 kHz, signed 16-bit PCM
  encoded to FLAC. Session metadata records the actual capture format and frame
  counts.
- Default mastering targets are -19 LUFS integrated loudness, following the
  Apple Podcasts recommendation for spoken word, 11 LU loudness range,
  -1.5 dBTP true peak, a -1 dBFS limiter ceiling after normalisation, two
  seconds of silence between parts, a mono downmix taken before the loudness is
  measured because -19 LUFS is the mono figure, and LAME variable bitrate at
  quality 5. A service is speech with
  pauses, so a constant bitrate spends bits on silence.
- Opening the MP3 folder is a local operating-system action initiated by the
  manager; keep the action visibly separate from creating the MP3.
