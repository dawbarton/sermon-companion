SERMON COMPANION: WINDOWS SETUP AND OPERATOR NOTES
=================================================

PACKAGE CONTENTS

Keep these files together in one folder:

  SermonCompanion.exe
  ffmpeg.exe
  ffprobe.exe
  Start Sermon Companion.cmd

SermonCompanion.exe can be double-clicked directly. The .cmd launcher does the
same thing and is kept only so that existing shortcuts to it still work.

The GitHub release ZIP contains the application, launcher, and these notes. For
licensing and supply-chain clarity it does not redistribute FFmpeg. Download a
trusted Windows FFmpeg build separately, version 4.4 or later, then copy
`ffmpeg.exe` and `ffprobe.exe` into this folder before first use.

The application stores recordings and settings separately under:

  %LOCALAPPDATA%\Sermon Companion

Replacing the application folder therefore does not replace any recordings or
session metadata.

Versions before 0.6.0 used %APPDATA%\Sermon Companion, in the roaming profile.
Nothing is moved automatically. If that folder is present the log reports it;
copy across config.json and any recordings still wanted, then delete it.


ONE-TIME TECHNICAL SETUP

1. Double-click SermonCompanion.exe. No window opens: the application runs in
   the background and puts an icon in the notification area beside the clock.
   Windows may hide it under the arrow there; drag it onto the taskbar so that
   the operator can see whether the application is running.

2. In OBS, choose View, Docks, Custom Browser Docks. Add:

     Dock name: Sermon recording
     URL:       http://127.0.0.1:8765/dock

   The local address is available only on this computer. It does not expose the
   recording controls to the church network or the internet.

3. In the dock, open the list at the top and choose the audio device associated
   with the HDMI capture. The choice is written to config.json and used again
   next time. Devices are identified by the identifier Windows gives them, not
   by name alone, so several devices with similar names are not confused.

4. Open:

     %LOCALAPPDATA%\Sermon Companion\config.json

   Set the top-level "church" value to the church name. This becomes the default
   for each new service, for example:

     "church": "St Mary's Church",

   Save the file and restart the application. Everything else in "capture" is
   already correct: 48000 Hz, 2 channels, 20 ms periods, and 10 seconds of
   buffer.

5. Make a short test recording. Confirm that the review page plays the expected
   church mix, not a microphone or silent device, and that it reports zero
   dropped frames.


NORMAL OPERATION

1. Double-click SermonCompanion.exe before opening OBS, unless it is already
   running. Its icon sits in the notification area; clicking that icon opens the
   review page. Starting it twice by mistake is harmless: the second copy opens
   the review page of the first and closes.
2. In the OBS dock, check that the list at the top names the HDMI capture, then
   click "Start Recording". The service is named after today's date, and can be
   renamed on the review page. If the list shows "Not connected", the device
   used last time is absent: choose the right one before starting. A recording
   will not begin against a device that is missing.
3. Click Start Reading, Start Sermon, and Start Q&A at the appropriate times.
   Starting one part automatically ends any open part. Clicking an active part
   ends it without starting another.
4. "Add marker" records a general note position without changing a segment.
5. At the end of the service, click "Stop service".
6. Click "Review Recordings" in the dock, click the notification-area icon, or
   open http://127.0.0.1:8765/ in a browser. Check and, if necessary, edit the
   service title and church, then click "Save details". Each segment has its own
   Play button, with First 5s and Last 5s for checking where it begins and ends.
   For coarse adjustments, drag a segment or either of its edges on the waveform.
   Use Zoom and Pan, or enter exact start and end times, for fine adjustments.
   Every change to a segment is saved as it is made. Segments stop when they meet
   a neighbour and cannot overlap. Use Add segment for a missed interval, or
   Remove for an unwanted one. Removed segments can be restored from the panel
   below the table. Untick any part that should remain visible but not be
   exported. "Silence between segments", below the list, sets the pause left
   between one part and the next; it starts at two seconds and changes the MP3
   alone.
7. Click "Create MP3". Parts sharing a label are measured together, so a sermon
   split in two to drop something from the middle keeps one volume, while
   differently labelled parts are levelled separately. The parts are then joined
   into one mono file. The result is named
   `YYYY-MM-DD-Church-Name.mp3`. Use "Open MP3 folder" to reveal it in File
   Explorer, or download it through the browser.

A finished service can be deleted with the ✕ beside it in the list on the
left, which removes its recording and its MP3s from the computer for good. A
service still recording cannot be deleted.

The complete FLAC remains available if an operator makes a mistake. Creating a
new MP3 never changes it. Recreating an MP3 keeps the requested simple filename;
the previous version is retained under `exports\previous`.

Recordings are large. When it starts, the application deletes any service older
than the `retentionDays` setting, 60 days by default, including the MP3s made
from it. Upload each MP3 to the church website soon after the service, which is
where the lasting copy belongs. Set `retentionDays` to 0 to keep every service
indefinitely.

If the dock displays a recording problem or any dropped frames, retain the
partial recording but do not assume it is complete. Give `capture.log` and the
session folder to the person maintaining the installation.

The messages that a console window used to show are on the log page, reached
from "Show log" in the notification-area icon's menu or from the bottom of the
review page's left-hand list. They are also written to:

  %LOCALAPPDATA%\Sermon Companion\logs\sermon-companion.log

Send that file along with the session folder when reporting a problem. To close
the application, use "Quit Sermon Companion" in the icon's menu, which stops any
recording in progress safely first.


RECOVERY

If the computer or application stops during a recording, restart Sermon
Companion. It marks the session as "interrupted", retains the partial FLAC, and
closes an open segment at the last valid audio sample. Review the end time before
exporting.


IMPORTANT HARDWARE TEST

Some Windows audio drivers permit only one program at a time to read a capture
device. Test OBS streaming and Sermon Companion recording simultaneously for a
complete rehearsal. If the companion cannot open the HDMI device, use a
dedicated OBS monitoring device temporarily. The intended permanent fallback is
an OBS raw-mix adapter, which will feed the companion exactly the audio already
being streamed without opening the HDMI device a second time.
