# User guide

This guide covers using Sermon Companion week by week. Installing it is covered
in the [README](../README.md#quick-start) and, in more detail, in
[WINDOWS-SETUP.md](WINDOWS-SETUP.md); every setting is described in
[CONFIGURATION.md](CONFIGURATION.md).

- [Recording a service](#recording-a-service)
- [Reviewing and creating the MP3](#reviewing-and-creating-the-mp3)
- [Uploading to the church website](#uploading-to-the-church-website)
- [Where the files are kept](#where-the-files-are-kept)
- [Running in the background](#running-in-the-background)

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

### Starting with the stream

`sermon-companion.lua`, in the application folder, is an OBS script that starts
Sermon Companion's recording when OBS starts streaming and stops it when the
stream stops, so that nobody has to remember the dock's Start Recording. Add it
once in OBS under **Tools → Scripts** with the **+** button. Its settings there
choose whether it follows the stream, OBS's own recording, or both; the address
is normally left as it is.

A recording already running when the stream starts is left alone, and the end of
the stream stops whatever is being recorded, without the dock's confirmation
question. The dock's buttons still work as before. What the script does, and any
problem it meets, such as Sermon Companion not running, is written to OBS's
script log (**Tools → Scripts → Script Log**); the dock shows the same problem by
failing to load.

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

The MP3 is a single channel, levelled to the loudness recommended for mono
spoken word; [`mastering.mono`](CONFIGURATION.md#mastering-the-mp3) keeps the
recorded stereo instead. It is named after the service date and the `church`
setting, for example `2026-08-30-St-Marys-Church.mp3`. Creating it again keeps
the previous one in an `exports/previous` folder. Changing a part after the MP3
has been created marks it out of date, so it is clear that it needs making
again. The talk's details on the upload page are not part of the MP3, and
changing them does not.

## Uploading to the church website

This needs setting up first; see
[CONFIGURATION.md](CONFIGURATION.md#uploading-to-the-church-website), which also
says which websites it works with.

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

A service can be deleted with the ✕ beside it in the list. That removes its
recording, its marks, and its MP3s from this computer for good.

Recordings are large, roughly 500 MB for a service. When it starts, the
application deletes every service older than 60 days, including its MP3s. The
MP3 belongs on the church website, not on this computer, so upload it before
then. Change or switch off that period with
[`retentionDays`](CONFIGURATION.md#general-settings).

Versions before 0.6.0 used `%APPDATA%\Sermon Companion`, in the roaming profile,
which is copied between machines and is no place for recordings this large.
Nothing is moved automatically. If that folder exists, the log says so; copy
across `config.json` and any recordings still wanted, then delete it.

## Running in the background

The application has no window of its own. It sits in the Windows notification
area, or the macOS menu bar, and stays there until it is closed:

- Clicking the icon opens the review page.
- Its menu offers the review page, **Show log**, and **Quit Sermon Companion**.
- Quitting closes any recording in progress safely first.

Starting a second copy by mistake opens the review page of the one already
running rather than starting again.

**Show log** displays the application's messages, which is what to look at first
if something did not work. It also names the version being run. The same page is
linked from the bottom of the review page's list of services, and the messages
are also written to `logs\sermon-companion.log` beside the recordings.

What to do after a power cut or a crash during a recording is described under
"Recovery" in [WINDOWS-SETUP.md](WINDOWS-SETUP.md).
