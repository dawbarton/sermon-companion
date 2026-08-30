# Sermon Companion

Sermon Companion records a church service and turns the useful parts of it into
a single MP3. It runs on the computer that already runs OBS, records the whole
service to a lossless file, and gives the operator a small panel inside OBS with
one button for each part of the service. Nothing has to be timed exactly,
because the complete recording is kept and every mark can be corrected
afterwards.

After the service, a review page shows the recording as a waveform. Drag the
marked parts into place, listen to them, then create the MP3. Each part is
levelled separately, so a quiet reading and a loud sermon end up at the same
comfortable volume.

## What you need

- Windows 10 or 11, with the service audio reaching the computer, usually
  through an HDMI capture device.
- OBS, for the recording dock. The review page works in any web browser.
- `ffmpeg.exe` and `ffprobe.exe`, version 4.4 or later. These are not included
  in the download; see the installation notes below.

Nothing is sent anywhere. The application listens only on this computer, and
there is no account, cloud service, or internet connection involved.

## Installing

1. Download the ZIP from the [latest release](https://github.com/dawbarton/sermon-companion/releases)
   and unpack it wherever you like, for example `C:\SermonCompanion`.
2. Download a trusted Windows build of FFmpeg and copy `ffmpeg.exe` and
   `ffprobe.exe` into the same folder, beside `SermonCompanion.exe`.
3. Double-click `Start Sermon Companion.cmd`. The review page opens in your web
   browser, and a small console window stays open while the application runs.
4. Add the dock to OBS: **Docks → Custom Browser Docks**, name it
   `Sermon recording`, and enter the address `http://127.0.0.1:8765/dock`.

[docs/WINDOWS-SETUP.md](docs/WINDOWS-SETUP.md) covers this in more detail,
including choosing the capture device and what to do if OBS and Sermon
Companion cannot share it.

Before the first service, run one short test recording end to end and listen to
the MP3.

## Recording a service

In the OBS dock:

1. Type a title, such as `Sunday morning service`, and press **Start continuous
   recording**. The whole service is now being recorded.
2. Press **Reading**, **Sermon**, or **Q&A** when each part begins. Pressing the
   next one ends the previous one. Press the same button again to end a part
   without starting another.
3. **Add marker** notes a moment you want to find later, without starting a
   part.
4. Press **Stop service** at the end.

Being a few seconds late with a button does not matter. The complete audio is
kept, and the times are adjusted afterwards. **Open review page** opens the
review and MP3 page in your web browser.

## Reviewing and creating the MP3

Choose the service in the list on the left. Then:

- Check and correct the service title and church, which name the MP3.
- Drag a marked part along the waveform to move it, or drag either edge to
  change where it starts or ends. Zoom in first for fine adjustments. Parts can
  touch but cannot overlap.
- Press **Play** beside a part to hear just that part, or type exact times into
  the Start and End boxes and press **Save**.
- Add a part that was missed, or remove one that is not wanted. Removing is
  reversible: removed parts stay listed and can be restored.
- Untick **Use** to keep a part in the record but leave it out of the MP3.
- Press **Create MP3**, then **Open MP3 folder** or **Download MP3**.

The MP3 is named after the service date and church, for example
`2026-08-30-St-Marys-Church.mp3`. Creating it again keeps the previous one in an
`exports/previous` folder. Changing anything after the MP3 has been created
marks it out of date, so it is clear that it needs making again.

## Where the files are kept

Recordings and settings live outside the application folder, in:

```text
%LOCALAPPDATA%\Sermon Companion
```

Each service has its own folder there, holding the lossless recording, the
marks, and the MP3s made from it. Replacing the application folder with a newer
release does not touch any of it.

Lossless recordings are large, roughly 500 MB for a service. The application
deletes recordings older than 60 days when it starts, keeping the service
details and any MP3 already created. Change or switch off that period with
`retentionDays` below.

## Settings

`config.json` is created in the folder above on first run. Edit it with a text
editor and restart the application. The settings most likely to need changing
are:

| Setting | Meaning |
| --- | --- |
| `church` | Church name for new services, used in the MP3 filename. |
| `capture.deviceId` | Recording device. Run `SermonCompanion.exe --list-devices` to see the available devices and their identifiers. |
| `retentionDays` | Days a lossless recording is kept. `0` keeps every recording indefinitely. |
| `mastering.mp3Quality` | MP3 size against quality, from `0` for the largest files to `9` for the smallest. The default, `5`, suits speech. |
| `mastering.integratedLUFS` | Loudness the finished MP3 is levelled to. `-16` suits speech played on a phone or laptop. |
| `listen` | Address the application serves on. Change the port here if `8765` is already in use, and in the OBS dock address to match. |

## Building from source

Requirements: Go 1.23 or later, a C compiler for cgo, and FFmpeg 4.4 or later
with FFprobe on `PATH`.

```sh
make test
make run-demo
```

`make run-demo` records a synthetic tone rather than a real device, so the
recording, editing, and MP3 paths can be exercised on any machine. It writes to
`work/demo-data` and serves the review page at <http://127.0.0.1:8765/> and the
dock at <http://127.0.0.1:8765/dock>. Omit `--demo` to record from a real input.

A self-contained Windows folder can be built from macOS, Linux, or Windows:

```powershell
pwsh -File scripts/build-windows.ps1 -FFmpegDirectory C:\path\to\ffmpeg\bin -CCompiler gcc
```

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) describes how the recording,
editing, and mastering paths fit together, and why they are built this way.

## Releases

Sermon Companion uses [Semantic Versioning](https://semver.org/), with the
current version in `VERSION`. Until 1.0, the minor version increases for new
functionality or an intentionally incompatible change, and the patch version for
corrections.

To publish a release, commit the intended `VERSION` value and push an annotated
tag with the same value prefixed by `v`, such as `v0.2.0`. The Windows workflow
rejects a mismatched or malformed tag, runs the tests with native capture
enabled, embeds the version in the executable, and creates the GitHub release.
Release builds report their version with `SermonCompanion.exe --version`, and
development builds report `dev`.
