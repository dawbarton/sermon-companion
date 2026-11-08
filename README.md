# Sermon Companion

[![CI](https://github.com/dawbarton/sermon-companion/actions/workflows/ci.yml/badge.svg)](https://github.com/dawbarton/sermon-companion/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/dawbarton/sermon-companion)](https://github.com/dawbarton/sermon-companion/releases/latest)

**Record the whole service, mark the sermon with one click in OBS, and end up
with a podcast-ready MP3, without anyone having to edit audio.**

Sermon Companion runs beside OBS on the computer that streams your services. It
records the whole service to a lossless file while whoever is on the A/V rota
presses a button in a small panel docked in OBS as the reading, sermon, or Q&A begins.
Afterwards, a review page in the web browser shows the recording as a waveform,
where the marked parts can be nudged into place, listened to, and turned into a
single MP3 at a consistent, comfortable volume. If you want, it then puts the
MP3 on the church website, with the title, speakers, series, and passage filled
in from the preaching programme.

## Why use it

- **Nothing has to be timed exactly.** The complete recording is kept, so a
  button pressed late, or not at all, is put right afterwards by dragging the
  part along the waveform.
- **Made for volunteers.** One button per part of the service inside OBS, an
  optional OBS script that starts and stops the recording with the stream, and a
  review page that saves as it goes.
- **An even volume.** Each part is levelled to the spoken-word podcast target, so
  a quiet reading and a loud sermon sit at the same level, and a sermon cut in two
  keeps one volume throughout.
- **Hard to lose a recording.** The original is never altered, a part removed
  by mistake can be restored, and a crash or power cut leaves a usable partial
  recording.
- **Private by default.** No account, no cloud service, and no OBS plugin to
  install. Nothing leaves the computer unless website upload is switched on.

Sermon Companion was written for one church's A/V team and is still at version
0.x, so please [say](#help-and-feedback) if something does not suit your church.

## What you need

- Windows 10 or 11, with the service audio reaching the computer, usually
  through the HDMI capture device OBS already uses.
- [OBS Studio](https://obsproject.com/), for the recording panel. The review
  page works in any web browser.
- FFmpeg 4.4 or later (`ffmpeg.exe` and `ffprobe.exe`), which is not included in
  the download.

## Quick start

1. Download the ZIP from the
   [latest release](https://github.com/dawbarton/sermon-companion/releases/latest)
   and unpack it wherever you like, for example `C:\SermonCompanion`.
2. Download a Windows build of FFmpeg from one of the sites linked on
   [ffmpeg.org](https://ffmpeg.org/download.html#build-windows), and copy
   `ffmpeg.exe` and `ffprobe.exe` from its `bin` folder into the same folder as
   `SermonCompanion.exe`.
3. Double-click `SermonCompanion.exe`. No window opens: the application runs in
   the background, with an icon in the notification area by the clock (perhaps
   under the **^** arrow). Click the icon to open the review page. The download
   is not code-signed, so Windows may first warn that it does not recognise it;
   choose **More info**, then **Run anyway**.
4. In OBS, choose **Docks → Custom Browser Docks** (**View → Docks** in older
   versions), name the dock `Sermon recording`, and enter the address
   `http://127.0.0.1:8765/dock`.
5. In the new dock, choose the HDMI capture device from the list at the top. The
   choice is remembered.
6. On the review page, click **Edit settings file**. In the file that opens, set
   `church` to your church's name, which names each MP3:

   ```json
   "church": "St Mary's Church",
   ```

   Save it, then restart Sermon Companion: choose **Quit Sermon Companion** in
   the icon's menu, then start `SermonCompanion.exe` again.
7. Make a short test recording with OBS streaming or recording at the same
   time, create the MP3, and listen to it. Some capture devices can be used by
   only one program at a time; "Important hardware test" in
   [WINDOWS-SETUP.md](docs/WINDOWS-SETUP.md) says what to do if yours is one of
   them.

To have recording start and stop with the stream, also add
`sermon-companion.lua`, from the application folder, in OBS under
**Tools → Scripts** with the **+** button.

## Using it

**During the service**, in the OBS dock: press **Start Recording** (or start the
stream, if the OBS script is set up), then **Tag Sermon start** and the other
buttons as each part begins, and **Stop service** at the end.

**Afterwards**, click **Review Recordings** in the dock or the notification-area
icon, and choose the service. Drag the marked parts into place on the waveform,
press **Play** to check them, then **Create MP3**. **Open MP3 folder** shows the
file, named like `2026-08-30-St-Marys-Church.mp3`.

The [user guide](docs/USER-GUIDE.md) covers everything else: splitting and
adding parts, levelling, uploading, deleting old services, and the log.

## Settings

Settings live in `%LOCALAPPDATA%\Sermon Companion\config.json`, which is created
on first run and opened by the review page's **Edit settings file** link. Save
the file and restart the application after changing it; a mistake is reported
at start-up rather than ignored. The settings most often changed are:

| Setting | Meaning |
| --- | --- |
| `church` | The church name, used in the MP3's name and on the website. |
| `retentionDays` | Days a service is kept before it is deleted, `60` by default; `0` keeps everything. A service is about 500 MB. |
| `mastering.gapSeconds` | Seconds of silence between parts in the MP3, `2` by default. |
| `listen` | The address and port the pages use, `127.0.0.1:8765` by default. |
| `publish` | Uploading to the church website; see below. |

[docs/CONFIGURATION.md](docs/CONFIGURATION.md) describes every setting.

## Uploading to the church website

With uploading set up, the review page's **Upload MP3** opens a page with the
talk's details already filled in from the preaching programme, the website, and
the church's YouTube channel. The operator checks them and presses **Publish**,
and the talk appears on the website without anyone signing in to it.

> [!IMPORTANT]
> Uploading works with the WordPress church websites built by
> [Nine Foot One](https://www.ninefootone.co.uk), which keep talks as a
> `resource` post type, with speakers, series, and Bible books as taxonomies and
> the audio in Advanced Custom Fields. As it stands, it will not work with a site
> laid out differently, or with a sermon plugin. If you would like it to work
> with yours, please
> [open an issue](https://github.com/dawbarton/sermon-companion/issues/new)
> describing your site.

To set it up, save an application password for an Author account in
`website-password.txt` beside `config.json`, and add a `publish` section to
`config.json`, for example after the `church` line:

```json
"publish": {
  "site": "https://www.example.org",
  "user": "uploads@example.org",
  "status": "private",
  "programme": "C:\\Users\\AV\\Documents\\Preaching programme.xlsx",
  "youtubeChannel": "UCxxxxxxxxxxxxxxxxxxxxxx"
},
```

`"status": "private"` keeps test uploads hidden; remove it once a test has
worked. [Uploading to the church website](docs/CONFIGURATION.md#uploading-to-the-church-website)
gives the steps in full, and [The preaching programme](docs/CONFIGURATION.md#the-preaching-programme)
the spreadsheet's columns.

## Help and feedback

Questions, problems, and ideas are all welcome as
[GitHub issues](https://github.com/dawbarton/sermon-companion/issues). It helps
to hear from anyone trying Sermon Companion at another church, including what
did not suit your set-up.

When reporting a problem, please say which version you are running (it is shown
on the log page, which **Show log** in the icon's menu opens), what you did, and
what happened. The log file, `%LOCALAPPDATA%\Sermon Companion\logs\sermon-companion.log`,
usually shows what went wrong. It never contains the website password, but it
can name your church, its website, and its speakers, so look through it before
attaching it.

## Documentation

- [User guide](docs/USER-GUIDE.md): recording, reviewing, creating and uploading
  the MP3, and where the files are kept.
- [Windows set-up and operator notes](docs/WINDOWS-SETUP.md): installation in
  detail, a week-by-week checklist, and recovery after a crash. The release ZIP
  includes it as `README.txt`.
- [Settings reference](docs/CONFIGURATION.md): every setting in `config.json`,
  uploading, and the preaching programme.
- [Architecture](docs/ARCHITECTURE.md): how recording, editing, and mastering
  fit together, and why.
- [Contributing](CONTRIBUTING.md): building from source, testing, and releases.

## Licence

Sermon Companion is released under the MIT Licence; see [LICENSE](LICENSE).
