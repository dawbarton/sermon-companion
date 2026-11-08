SERMON COMPANION: WINDOWS SETUP AND OPERATOR NOTES
=================================================

The full documentation, including a reference for every setting, is at
https://github.com/dawbarton/sermon-companion. Problems and questions are
welcome there as issues.

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

2. In OBS, choose Docks, Custom Browser Docks (View, Docks in older versions
   of OBS). Add:

     Dock name: Sermon recording
     URL:       http://127.0.0.1:8765/dock

   The local address is available only on this computer. It does not expose the
   recording controls to the church network or the internet.

   To have the recording start and stop with the stream, also choose Tools,
   Scripts in OBS, click +, and pick sermon-companion.lua from the Sermon
   Companion folder. "Follow the stream" is ticked already; tick "Follow OBS's
   own recording" as well if the service is sometimes recorded in OBS without
   being streamed. Leave the address as it is.

3. In the dock, open the list at the top and choose the audio device associated
   with the HDMI capture. The choice is written to config.json and used again
   next time. Devices are identified by the identifier Windows gives them, not
   by name alone, so several devices with similar names are not confused.

4. Open the settings file. On the review page, click "Edit settings file" beneath
   the application log link; it opens in Notepad. It can also be opened by hand:

     %LOCALAPPDATA%\Sermon Companion\config.json

   Set the top-level "church" value to the church name, spelt exactly as the
   church website spells it if talks are to be uploaded. It names each MP3 and
   is the church a talk is filed under, for example:

     "church": "Emmanuel Bishopston",

   Save the file and restart the application. Everything else in "capture" is
   already correct: 48000 Hz, 2 channels, 20 ms periods, and 10 seconds of
   buffer. Setting names and numeric ranges are checked at start-up; a typo is
   shown as an error instead of being silently ignored.

5. Make a short test recording. Confirm that the review page plays the expected
   church mix, not a microphone or silent device, and that it reports zero
   dropped frames.

6. To upload talks to the church website, follow "SETTING UP UPLOADING" below.


NORMAL OPERATION

1. Double-click SermonCompanion.exe before opening OBS, unless it is already
   running. Its icon sits in the notification area; clicking that icon opens the
   review page. Starting it twice by mistake is harmless: the second copy opens
   the review page of the first and closes.
2. In the OBS dock, check that the list at the top names the HDMI capture, then
   click "Start Recording". Where the OBS script is set up, starting the stream
   does this instead, and stopping the stream ends the recording without the
   usual question; the dock then shows the recording as stopped. The service is named after today's date until its
   talk is given a title on the upload page. If the list shows "Not connected", the device
   used last time is absent: choose the right one before starting. A recording
   will not begin against a device that is missing.
3. Click Tag Reading start, Tag Sermon start, and Tag Q&A start at the
   appropriate times. Starting one part automatically ends any open part.
   Clicking its Tag … end button ends it without starting another.
4. "Add marker" records a general note position without changing a segment.
5. At the end of the service, click "Stop service".
6. Click "Review Recordings" in the dock, click the notification-area icon, or
   open http://127.0.0.1:8765/ in a browser. Each segment has
   its own Play button, with First 5s and Last 5s for checking where it begins and ends.
   For coarse adjustments, drag a segment or either of its edges on the waveform.
   Use Zoom and Pan, or enter exact start and end times, for fine adjustments.
   Put the playback cursor inside a segment and use Split at cursor to divide it;
   both resulting parts keep the same label and Use setting.
   Every change to a segment is saved as it is made. Segments stop when they meet
   a neighbour and cannot overlap. Use Add segment for a missed interval, or
   Remove for an unwanted one. Removed segments can be restored from the panel
   below the table. Untick any part that should remain visible but not be
   exported. "Silence between segments", below the list, sets the pause left
   between one part and the next; it starts at two seconds and changes the MP3
   alone.
7. Click "Create MP3". If no parts were marked, the whole recording is
   normalised and exported. Parts sharing a label are measured together, so a
   sermon split in two to drop something from the middle keeps one volume, while
   differently labelled parts are levelled separately. The parts are then joined
   into one mono file. The result is named
   `YYYY-MM-DD-Church-Name.mp3`. Use "Open MP3 folder" to reveal it in File
   Explorer, or download it through the browser.
8. Where uploading is set up, click "Upload MP3" instead of step 7. It makes
   the MP3 if needed and opens the upload page at once. The talk's title,
   speakers, series, Bible passage, image, and YouTube video are filled in from
   the preaching programme, the website, and YouTube; the line at the top says
   where each came from. Correct anything that changed late: each field saves
   when it is left. Deal with anything listed in red, such as a speaker the
   website does not know: choose the suggested name if it is a typing slip, or
   tick "This is a new speaker" if it is not. Then click "Publish". The upload
   carries on if the page is closed. If it stops, for example because the
   internet connection dropped, click "Publish again"; it carries on from where
   it stopped and never creates a talk twice. When it has finished, the page
   shows the talk's link on the website. To correct a published talk, sign in
   to the website.

The editing controls are disabled while the MP3 is being created. If the
application is closed during this work, it cancels the export and shows it as
failed when reopened; click "Create MP3" again.

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
session folder to the person maintaining the installation. The error remains in
the dock until a later recording starts successfully, so it cannot disappear
between status polls.

The messages that a console window used to show are on the log page, reached
from "Show log" in the notification-area icon's menu or from the bottom of the
review page's left-hand list. That page also names the version being run, which
is what to quote when reporting a problem: the application has no console, so
typing SermonCompanion.exe --version at a prompt shows nothing. They are also
written to:

  %LOCALAPPDATA%\Sermon Companion\logs\sermon-companion.log

Send that file along with the session folder when reporting a problem. To close
the application, use "Quit Sermon Companion" in the icon's menu, which stops any
recording in progress safely first.


SETTING UP UPLOADING

Uploading works with the WordPress church websites built by Nine Foot One
(https://www.ninefootone.co.uk). A website laid out differently, or one using
a sermon plugin, is not supported.

1. On the church website, choose the account that will upload. It needs the
   Author role and nothing more. Under Users, Profile, Application Passwords,
   create a password named "Sermon Companion" and copy it.

2. Save the password, exactly as shown, in a new text file called

     %LOCALAPPDATA%\Sermon Companion\website-password.txt

   beside config.json. Nothing else goes in the file. Sermon Companion reads it
   only when it uploads and never displays it. To withdraw the computer's
   access, revoke the password on the website.

3. Add a "publish" section to config.json, beside "church":

     "publish": {
       "site": "https://www.emmanuelbristol.org.uk",
       "user": "the account's user name or email address",
       "programme": "C:\\Users\\AV\\Emmanuel\\Preaching programme.xlsx",
       "youtubeChannel": "UCReU3dVxdLKPuSs59dBgG6Q"
     },

   Backslashes in a path are written twice. "programme" and "youtubeChannel"
   may be left out. Add "status": "private" while testing, so that uploads are
   visible only to people signed in to the website. Save the file and restart
   the application. The review page now shows "Upload MP3".

4. The preaching programme is a spreadsheet with a heading row containing at
   least Date, and usually Service (the start time), Title, Speakers, Series,
   Passage, Series image, and Podcast. Columns may be in any order, and any
   others are ignored. Separate several speakers with commas. "Series image" is
   a link to the series' picture on the website, needed only for the first talk
   of a new series. Put No under "Podcast" to keep a talk out of the podcast
   feed; leave it empty otherwise. If the programme lives in SharePoint, sync its folder through
   OneDrive, right-click the file in File Explorer, choose "Always keep on this
   device", and give that local path in config.json.

5. A site that runs the Defender security plugin needs a short WPCode snippet
   before application passwords work: without it every upload fails with a
   message that the website ignored the sign-in. What the snippet has to do,
   and why, is described under "Credentials" in docs/SERMON-UPLOAD-DESIGN.md,
   at the address at the top of these notes.

6. Try a private upload of a test recording, check it on the website while
   signed in, and delete it there.


RECOVERY

If the computer or application stops during a recording, restart Sermon
Companion. It marks the session as "interrupted", retains the partial FLAC,
measures how much audio reached the file, and closes an open segment at the last
valid audio sample. Rather less audio usually reaches the file than was
recorded, because the encoder had not written its last few seconds, so a segment
marked just before the interruption may be shortened or removed; removed
segments can be restored from the review page. The review page explains what
happened above the player. Review the end times before exporting.

If the application stops during an upload, the upload page shows it as stopped
when the application is next started. Click "Publish again" to carry on.


IMPORTANT HARDWARE TEST

Some Windows audio drivers permit only one program at a time to read a capture
device. Test OBS streaming and Sermon Companion recording simultaneously for a
complete rehearsal. If the companion cannot open the HDMI device, use a
dedicated OBS monitoring device temporarily. The intended permanent fallback is
an OBS raw-mix adapter, which will feed the companion exactly the audio already
being streamed without opening the HDMI device a second time.
