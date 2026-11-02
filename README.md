# Sermon Companion

Sermon Companion records a church service and turns the useful parts of it into
a single MP3. It runs on the computer that already runs OBS, records the whole
service to a lossless file, and gives the operator a small panel inside OBS with
one button for each part of the service. Nothing has to be timed exactly,
because the complete recording is kept and every mark can be corrected
afterwards.

After the service, a review page shows the recording as a waveform. Drag the
marked parts into place, listen to them, then create the MP3. Each part is
levelled by its label, so a quiet reading and a loud sermon end up at the same
comfortable volume, while a sermon split in two keeps one volume throughout.

If it is set up to, it then publishes the MP3 on the church's WordPress website
as a talk, with its title, speakers, series, passage, image, and video filled in
from the church's preaching programme, so that whoever is on the A/V rota that
week does not need to sign in to the website.

## What you need

- Windows 10 or 11, with the service audio reaching the computer, usually
  through an HDMI capture device.
- OBS, for the recording dock. The review page works in any web browser.
- `ffmpeg.exe` and `ffprobe.exe`, version 4.4 or later. These are not included
  in the download; see the installation notes below.

The application listens only on this computer. Unless uploading to the church
website is set up, nothing is sent anywhere and no internet connection is
involved. With it set up, the application talks to that one website, signed in
with an application password kept beside the settings, and reads the church
channel's public list of YouTube videos; see
[Uploading to the church website](#uploading-to-the-church-website).

## Installing

1. Download the ZIP from the [latest release](https://github.com/dawbarton/sermon-companion/releases)
   and unpack it wherever you like, for example `C:\SermonCompanion`.
2. Download a trusted Windows build of FFmpeg and copy `ffmpeg.exe` and
   `ffprobe.exe` into the same folder, beside `SermonCompanion.exe`.
3. Double-click `SermonCompanion.exe`. Nothing appears to happen: the
   application runs in the background, with an icon in the notification area by
   the clock. Click that icon to open the review page.
4. Add the dock to OBS: **Docks → Custom Browser Docks**, name it
   `Sermon recording`, and enter the address `http://127.0.0.1:8765/dock`.
5. In the OBS dock, choose the HDMI capture device from the list at the top. The
   choice is remembered for future services.

[docs/WINDOWS-SETUP.md](docs/WINDOWS-SETUP.md) covers this in more detail,
including choosing the capture device and what to do if OBS and Sermon
Companion cannot share it.

Before the first service, run one short test recording end to end and listen to
the MP3.

## Recording a service

In the OBS dock:

1. Check that the device list at the top names the HDMI capture, then press
   **Start Recording**. The whole service is now being recorded. The service is
   named after today's date until its talk is given a title on the upload page.
2. Press **Tag Reading start**, **Tag Sermon start**, or **Tag Q&A start** when
   each part begins. Pressing the next start button ends the previous part. Press
   its **Tag … end** button to end a part without starting another.
3. **Add marker** notes a moment you want to find later, without starting a
   part.
4. Press **Stop service** at the end.

Being a few seconds late with a button does not matter. The complete audio is
kept, and the times are adjusted afterwards. **Review Recordings** opens the
review and MP3 page in your web browser, as does clicking the notification-area
icon.

If the device list shows **⚠ Not connected**, the device chosen last time is not
there now. Pick the right one from the list before starting; recording will not
begin against a device that is missing.

The dock keeps a recording failure visible until the next recording starts
successfully. A recording is shown as started only after the capture process has
reported audio and the session metadata has been saved.

## Reviewing and creating the MP3

Choose the service in the list on the left. Then:

- Drag a marked part along the waveform to move it, or drag either edge to
  change where it starts or ends. Zoom in first for fine adjustments. Parts can
  touch but cannot overlap.
- Press **Play** beside a part to hear it, or **First 5s** and **Last 5s** to
  hear only where it begins and ends while adjusting those points.
- Exact times can be typed into the Start and End boxes. Every change to a part
  is saved as it is made, so there is nothing to press afterwards.
- Put the playback cursor inside a part and press **Split at cursor** to divide
  it there. Both parts keep the same label and **Use** setting. The button is
  available only when at least 0.1 seconds will remain on each side.
- Add a part that was missed, or remove one that is not wanted. If the playback
  cursor is inside an existing part, **Add segment** starts at the first free
  time after it. Removing is reversible: removed parts stay listed and can be
  restored.
- Untick **Use** to keep a part in the record but leave it out of the MP3.
- Parts given the same label are levelled together. Cutting something out of the
  middle of a sermon leaves two parts both labelled Sermon, and they come out at
  one volume rather than as two halves at different levels. Give a part its own
  label to have it levelled on its own.
- **Silence between segments**, below the list, sets the pause left between one
  part and the next in the MP3. It starts at two seconds, changes only the MP3,
  and is remembered for that service alone.
- Press **Create MP3**, then **Open MP3 folder** or **Download MP3**. If no parts
  have ever been marked, the whole recording is normalised as one part and
  exported. Parts deliberately removed or unticked are still respected. Where
  uploading is set up, **Upload MP3** takes the place of **Download MP3**; see
  below.

The service is read-only while its MP3 is being created. Closing Sermon
Companion cancels any active export cleanly and records it as failed, ready to
be started again after reopening the application.

The MP3 is a single channel: a service is one mix of one speaker, so both
recorded channels carry the same audio, and `-19` is the loudness recommended
for a mono spoken-word file. `mastering.mono` below keeps the recorded stereo
instead. The lossless recording stays stereo either way.

The MP3 is named after the service date and the `church` setting, for example
`2026-08-30-St-Marys-Church.mp3`. Creating it again keeps the previous one in an
`exports/previous` folder. Changing a part after the MP3 has been created marks
it out of date, so it is clear that it needs making again. The talk's details on
the upload page are not part of the MP3, and changing them does not.

## Uploading to the church website

Press **Upload MP3** on the review page. It starts making the MP3 if there is no
up-to-date one, and opens the upload page straight away, so the details can be
checked while the MP3 is made.

The first time the page is opened for a service, it fills the details in:

- the title, speakers, series, passage, series image, and podcast choice from
  the preaching programme's row for the day of the service, choosing by service
  time when there is more than one that day;
- the series of the church's latest talk on the website, if the programme gives
  none;
- the video from the church's YouTube channel, the first published around the
  time of the recording;
- the image used by the series' latest talk on the website, unless the
  programme names one.

A line at the top says where each came from. Each field saves itself when it is
left. Beside them, the page says what the website will make of the talk: a
speaker or series the website does not have, with any similar names it does have
to choose instead and a box to tick if the name really is new; the Bible books
the passage will be filed under; the image; the video's title; and any talk
already on the website for the same church and day. **Publish** becomes
available once the MP3 is ready and nothing stands in the way, and asks before
it publishes. The upload carries on if the page is closed.

If the upload stops part way, for example because the internet connection
dropped, press **Publish again**: it carries on from where it stopped without
creating anything twice. Once the talk is published, its link is shown, and any
later correction is made on the website itself.

### Setting it up

Add a `publish` section to `config.json`:

```json
"publish": {
  "site": "https://www.example.org",
  "user": "uploads@example.org",
  "programme": "C:\\Users\\AV\\Church\\Preaching programme.xlsx",
  "youtubeChannel": "UCxxxxxxxxxxxxxxxxxxxxxx"
}
```

| Setting | Meaning |
| --- | --- |
| `publish.site` | The website's address, which must begin `https://`. |
| `publish.user` | The WordPress account that uploads. It needs the Author role. |
| `publish.status` | `publish`, the default, or `private` or `draft` for testing. |
| `publish.podcast` | Whether a talk goes into the podcast feed unless the box is unticked. The default is `true`. |
| `publish.resourceType` | The website's name for the kind of resource a talk is. The default is `Sermon`. |
| `publish.programme` | The preaching programme, an `.xlsx` or `.csv` file. Optional. |
| `publish.programmeSheet` | The worksheet to read; the first is used if this is left out. |
| `publish.youtubeChannel` | The channel's ID, beginning `UC`, not its @ handle. Optional. |

Then create an application password for that account on the website (Users,
Profile, Application Passwords) and save it in a file called
`website-password.txt` beside `config.json`. It is read only when needed, never
shown, and kept out of `config.json` so that the settings can be opened on
screen safely. A WordPress site running Defender needs a small extra snippet
before application passwords work at all; see
[docs/SERMON-UPLOAD-DESIGN.md](docs/SERMON-UPLOAD-DESIGN.md).

### The preaching programme

The programme is a spreadsheet with a heading row. Columns are found by their
headings, so they can be in any order, and columns with other headings are
ignored:

| Heading | Contents |
| --- | --- |
| Date | The day of the service. Required. |
| Service | The start time, such as 10:30, used to choose between two services on one day. |
| Church | Only needed when several churches share one programme. |
| Title | The talk's title. |
| Speakers | One or more names, separated by commas. |
| Series | The series name, as the website spells it. |
| Passage | Such as `Matthew 6:9-13`, or several separated by commas. |
| Series image | A link to the series' image on the website, needed only for the first talk of a new series. |
| Podcast | Yes or No. No keeps that talk out of the podcast feed; left empty, `publish.podcast` decides. |

A row whose date cannot be read is listed on the upload page rather than
skipped silently. A programme kept in SharePoint and synced to the church
computer through OneDrive should be set to "Always keep on this device", so that
it can be read when the computer is offline.

If the same service is open in two browser tabs, a save from an older view is
refused and that tab reloads the latest values. It never silently overwrites a
newer edit.

## Where the files are kept

Recordings and settings live outside the application folder, in:

```text
%LOCALAPPDATA%\Sermon Companion
```

Each service has its own folder there, holding the lossless recording, the
marks, and the MP3s made from it. Replacing the application folder with a newer
release does not touch any of it.

Versions before 0.6.0 used `%APPDATA%\Sermon Companion`, in the roaming profile,
which is copied between machines and is no place for recordings this large.
Nothing is moved automatically. If that folder exists, the log says so; copy
across `config.json` and any recordings still wanted, then delete it.

A service can be deleted with the ✕ beside it in the list. That removes its
recording, its marks, and its MP3s from this computer for good.

Recordings are large, roughly 500 MB for a service. When it starts, the
application deletes every service older than 60 days, including its MP3s. The
MP3 belongs on the church website, not on this computer, so upload it before
then. Change or switch off that period with `retentionDays` below.

## Settings

`config.json` is created in the folder above on first run. The review page has an
Edit settings file link, beneath the application log link, which opens it in a
text editor: Notepad on Windows, the default plain-text editor on macOS. Save the
file and restart the application for the changes to take effect. The settings
most likely to need changing are:

| Setting | Meaning |
| --- | --- |
| `church` | Church name for new services, used in the MP3 filename and as the talk's church on the website. |
| `capture.deviceId` | Recording device. Normally set by choosing a device in the OBS dock rather than by hand. |
| `retentionDays` | Days a recorded service and its MP3s are kept before being deleted. `0` keeps every service indefinitely. |
| `mastering.mp3Quality` | MP3 size against quality, from `0` for the largest files to `9` for the smallest. The default, `5`, suits speech. |
| `mastering.integratedLUFS` | Loudness the finished MP3 is levelled to. The default, `-19`, follows the Apple Podcasts recommendation for spoken word. |
| `mastering.mono` | Whether the MP3 is a single channel. The default, `true`, suits one speaker through one mix and matches the loudness target above. Set to `false` to keep the recorded stereo. |
| `mastering.peakLimitDB` | Ceiling, in dBFS, that the levelled audio is limited to, guarding against clipping. The default is `-1`. |
| `mastering.gapSeconds` | Silence between parts in the MP3 for a new service, in seconds. The default is `2`, and a service can be given its own value on the review page. |
| `publish` | Uploading to the church website; see [Setting it up](#setting-it-up). |
| `listen` | Address the application serves on. Change the port here if `8765` is already in use, and in the OBS dock address to match. |

The application checks every setting before it starts recording or applies
retention. Misspelt setting names and unsafe numeric values are reported as a
start-up error rather than being silently replaced with defaults.

## Running in the background

The application has no window of its own. It sits in the Windows notification
area, or the macOS menu bar, and stays there until it is closed:

- Clicking the icon opens the review page.
- Its menu offers the review page, **Show log**, and **Quit Sermon Companion**.
- Quitting closes any recording in progress safely first.

Starting a second copy by mistake opens the review page of the one already
running rather than starting again.

**Show log** displays the messages that a console window used to carry, which is
what to look at first if something did not work. It also names the version being
run. The same page is linked from
the bottom of the review page sidebar, and the messages are also written to
`logs\sermon-companion.log` beside the recordings.

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
Release builds report their version on the log page, and with
`SermonCompanion.exe --version` when that output is redirected to a file or
another program. Development builds report `dev`. The application is linked
without a console on Windows, so a version typed at an interactive prompt has
nowhere to appear: read it from the log page instead.

## Licence

Sermon Companion is released under the MIT Licence; see [LICENSE](LICENSE).
