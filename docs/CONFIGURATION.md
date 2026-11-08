# Settings reference

Sermon Companion keeps its settings in `config.json`, which it creates the first
time it runs:

```text
%LOCALAPPDATA%\Sermon Companion\config.json
```

On macOS the folder is `~/Library/Application Support/Sermon Companion`.

- [Editing the file](#editing-the-file)
- [General settings](#general-settings)
- [Capture](#capture)
- [Dock buttons](#dock-buttons)
- [Mastering the MP3](#mastering-the-mp3)
- [Uploading to the church website](#uploading-to-the-church-website)
- [The preaching programme](#the-preaching-programme)

## Editing the file

The review page's **Edit settings file** link, beneath **Application log**, opens
the file in Notepad. If the application will not start, open the file from the
folder above instead: paste `%LOCALAPPDATA%\Sermon Companion` into File
Explorer's address bar.

The file is JSON. When editing it by hand:

- Text goes in double quotes; numbers, `true`, and `false` do not.
- Entries are separated by commas, with no comma after the last entry inside a
  pair of braces.
- A backslash in a Windows path is written twice, as in
  `"C:\\Users\\AV\\Preaching programme.xlsx"`.

Save the file, then restart the application for the change to take effect: use
**Quit Sermon Companion** in the notification-area icon's menu, then start it
again. Every setting is checked at start-up. A misspelt setting name or a value
out of range stops the application with a message naming the setting, rather
than being replaced silently by a default.

The application rewrites the file itself when a capture device is chosen in the
dock. Your settings are kept, but not any ordering or spacing you gave them.

## General settings

| Setting | Default | Meaning |
| --- | --- | --- |
| `church` | `"Church"` | The church name for new services. It names each MP3, such as `2026-08-30-St-Marys-Church.mp3`, and is the church a talk is filed under on the website, so spell it as the website does. |
| `retentionDays` | `60` | Days a recorded service and its MP3s are kept. Older services are deleted when the application starts. `0` keeps every service indefinitely. |
| `listen` | `"127.0.0.1:8765"` | The address and port the pages are served on. Change the port if `8765` is already in use, and change the OBS dock's and the OBS script's addresses to match. Pages are opened by IP address or `localhost`; a request addressed to any other name is refused unless it is the host given here. |
| `ffmpeg`, `ffprobe` | `"ffmpeg"`, `"ffprobe"` | The FFmpeg programs. With these defaults, `ffmpeg.exe` and `ffprobe.exe` beside `SermonCompanion.exe` are used if they are there, and those on `PATH` otherwise. Give a full path to use another copy. |
| `publish` | absent | Uploading to the church website; see [below](#uploading-to-the-church-website). Without it nothing is ever sent anywhere. |

A service's recording is about 500 MB. The MP3 belongs on the church website,
not on this computer, so upload it before `retentionDays` runs out. A recording
in progress is never deleted.

## Capture

The `capture` section says what is recorded and how. Choosing a device in the
OBS dock is the normal way to set it. The other values are correct for an HDMI
capture device and are best left alone.

| Setting | Default | Meaning |
| --- | --- | --- |
| `capture.deviceId` | absent | The recording device's identifier, written by the dock. Starting a recording fails, rather than recording something else, if this device is missing. |
| `capture.device` | a reminder to choose one | The device's name, written by the dock. It is used to find the device when `deviceId` is empty, and by the FFmpeg backend. |
| `capture.backend` | `"miniaudio"` | `miniaudio` records directly from the device. `ffmpeg` records through FFmpeg instead, a fallback for diagnosing a device problem. |
| `capture.driver` | `"dshow"` on Windows | The FFmpeg input used by the `ffmpeg` backend: `dshow`, `avfoundation`, `lavfi` for a synthetic test tone, or `custom`. |
| `capture.inputArgs` | absent | FFmpeg input arguments for the `custom` driver, as a list of strings. |
| `capture.sampleRate` | `48000` | Samples per second. |
| `capture.channels` | `2` | Recorded channels. The lossless recording keeps them all. |
| `capture.periodMilliseconds` | `20` | How often the device hands over audio. |
| `capture.bufferSeconds` | `10` | Audio held in memory while it is written to disk. Running out is reported as a recording failure, never hidden. |

## Dock buttons

`presets` lists the parts of a service the dock has buttons for. Each has a
`label`, shown on its buttons and given to the parts it marks, and a `kind`, a
short identifier that must be different for each preset. The default is:

```json
"presets": [
  { "kind": "reading", "label": "Reading" },
  { "kind": "sermon", "label": "Sermon" },
  { "kind": "questions", "label": "Q&A" }
]
```

Each preset adds a pair of buttons to a dock that shares a narrow column with
OBS's other docks, so add one only for a part that is marked every week. A part
can be given any label on the review page.

## Mastering the MP3

The `mastering` section says how the MP3 is made. Each part is levelled with
FFmpeg's two-pass `loudnorm`, parts with the same label together, and the parts
are joined with a short silence between them.

| Setting | Default | Meaning |
| --- | --- | --- |
| `mastering.integratedLUFS` | `-19` | The loudness the MP3 is levelled to, following the Apple Podcasts recommendation for spoken word. Between `-70` and `-5`. |
| `mastering.loudnessRangeLU` | `11` | The loudness range `loudnorm` aims for. Between `1` and `50`. |
| `mastering.truePeakDB` | `-1.5` | The true-peak level `loudnorm` aims for, in dBTP. Between `-9` and `0`. |
| `mastering.peakLimitDB` | `-1` | The ceiling, in dBFS, a limiter holds the levelled audio below, guarding against clipping. Between `-24` and `0`. |
| `mastering.mono` | `true` | Whether the MP3 is a single channel. A service is one mix, so both recorded channels carry the same audio, and `-19` is the figure for a mono file. Set to `false` to keep the recorded stereo; the lossless recording is stereo either way. |
| `mastering.gapSeconds` | `2` | Silence between parts in the MP3 for a new service, up to `30`. A service can be given its own value on the review page. |
| `mastering.mp3Quality` | `5` | MP3 size against quality, from `0` for the largest files to `9` for the smallest. The default suits speech. |

## Uploading to the church website

Uploading is switched on by adding a `publish` section. It works with the
WordPress church websites built by [ninefootone.co.uk](https://www.ninefootone.co.uk)
and should be straightforward to modify for other WordPress sites. Those
websites keep talks as a `resource` post type, with speakers, series, church,
and Bible book as taxonomies and the audio and passage in Advanced Custom Fields
writable over the REST API; a site laid out differently, or one using a sermon
plugin, needs changes to `internal/publish`. Please
[open an issue](https://github.com/dawbarton/sermon-companion/issues)
describing your site if you would like it to work with yours.

### Setting it up

1. On the website, choose the account that will upload. It needs the Author
   role and nothing more, and is best used for nothing else. Under **Users →
   Profile → Application Passwords**, create a password named
   `Sermon Companion` and copy it.
2. Save that password, and nothing else, in a new text file called
   `website-password.txt` beside `config.json`. It is read only when uploading,
   never shown, never logged, and kept out of `config.json` so that the
   settings can be opened on screen safely. To withdraw the computer's access,
   revoke the password on the website.
3. Add a `publish` section to `config.json`, for example after the `church`
   line:

   ```json
   "church": "St Mary's Church",
   "publish": {
     "site": "https://www.example.org",
     "user": "uploads@example.org",
     "status": "private",
     "programme": "C:\\Users\\AV\\Documents\\Preaching programme.xlsx",
     "youtubeChannel": "UCxxxxxxxxxxxxxxxxxxxxxx"
   },
   ```

4. Restart the application. The review page now offers **Upload MP3** in place
   of **Download MP3**.
5. Upload a short test recording. With `"status": "private"` it is visible only
   to people signed in to the website. Check it there, delete it, and remove the
   `status` line so that later uploads are published.

| Setting | Default | Meaning |
| --- | --- | --- |
| `publish.site` | required | The website's address, beginning `https://`. |
| `publish.user` | required | The WordPress account's user name or email address. |
| `publish.status` | `"publish"` | `publish`, or `private` or `draft` for testing. |
| `publish.podcast` | `true` | Whether a talk goes into the site's podcast feed unless its box is unticked on the upload page. |
| `publish.resourceType` | `"Sermon"` | The website's name for the kind of resource a talk is. |
| `publish.programme` | absent | The preaching programme, an `.xlsx` or `.csv` file; see below. |
| `publish.programmeSheet` | the first | The worksheet of the programme to read. |
| `publish.youtubeChannel` | absent | The channel whose video is offered for each talk. This is the channel ID, beginning `UC` and 24 characters long, not its @ handle; YouTube Studio shows it under **Settings → Channel → Advanced settings**. |

With uploading set up, the application signs in to that one website and reads
the YouTube channel's public list of videos. It sends nothing anywhere else.

A site running the Defender security plugin refuses application passwords until
a small snippet is added to it: every upload then fails with a message that the
website ignored the sign-in. What the snippet has to do, and why, is described
under "Credentials" in [SERMON-UPLOAD-DESIGN.md](SERMON-UPLOAD-DESIGN.md#credentials).

## The preaching programme

The upload page fills a talk's details in from the preaching programme's row for
the day of the service. The programme is a spreadsheet, `.xlsx` or `.csv`, with a
heading row. Columns are found by their headings, so they can be in any order,
and columns with other headings are ignored.

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

A row whose date cannot be read is listed on the upload page rather than skipped
silently. A programme kept in SharePoint should be synced to the computer
through OneDrive and set to **Always keep on this device** (right-click it in
File Explorer), so that it can be read when the computer is offline; give that
local path in `publish.programme`.
