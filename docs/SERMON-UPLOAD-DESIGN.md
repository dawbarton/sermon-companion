# Automatic sermon upload: working design

This is a working note, not a settled specification. It records what uploading a
finished MP3 to a church website needs, what was found out, and what was built:
a standalone publisher, `cmd/sermon-publish`, and an upload page in the
application itself, both over `internal/publish`. The Emmanuel Bristol site was probed
anonymously on 13 September 2026; what that established, and what it could not,
is set out under "What the site turned out to be" and in the open questions at
the end.

## Goal and deliberate non-goals

After a service, the operator presses Upload MP3 on the review page, checks the
details on the upload page, and presses Publish, and the MP3, with its title,
speakers, series, Bible passage, image, and video, arrives on the church
website as a published sermon. The details are filled in from the preaching
programme, the website, and YouTube, so the operator confirms them rather than
typing them. Nobody has to log in to the website.

The MP3 is the existing export of the segments the operator included: the
sermon, with the reading and Q&A when they are marked. The whole service is
never uploaded, and the export path needs no change for this.

A talk is uploaded every week, but by different members of the A/V team, each of
whom may do it only once every month or two. The design is judged by whether someone who has forgotten how it
works still gets it right, which rules out a draft that someone must then
publish on the site: logging in is the step being removed. The check a draft
would have provided moves into the app: the upload page shows everything that
will be published, says what the website will make of each name, and asks
before publishing. Publishing is not cheap to undo. The podcast
flag is on by default, and the site links to Apple Podcasts and Spotify shows
for each congregation, so a published talk very probably reaches podcast apps,
where a correction on the site does not follow it quickly. How those feeds are
produced has not been checked.

Not in scope for now:

- Building a podcast feed, social posts, or transcripts. The website is one
  integration; building a general publishing framework before a single site works
  is the wrong order.
- Uploading the lossless source. The MP3 is the published artefact.
- Correcting a talk once it is published, or replacing its audio. Both are made
  on the website for now.

## Build order

The website interface is the uncertain part, and the recording path is the part
that must not break. They are therefore separated for as long as possible.

1. **Reconnaissance.** `cmd/wp-probe` reads the site's REST API and reports the
   post type talks live in, the taxonomies holding a speaker or a series, the
   fields the site accepts, and what a given account may write. It makes only
   read requests. It exists, and has been run anonymously against the site.
2. **A standalone publisher.** `cmd/sermon-publish` takes an existing session
   directory and its exported MP3 and performs the upload, with `--dry-run`
   printing the exact requests it would make. It reads `session.json` through
   `internal/store` and writes nothing back. The main application is untouched,
   so the whole website interface can be got wrong repeatedly on a weekday
   without any risk to Sunday.
3. **Metadata collection in the app.** Only once the field set is confirmed by
   step 2 is the session model extended and the review page given the fields.
   Getting this order backwards means designing the form before knowing what it
   has to produce.
4. **The button.** The review page gains an upload action, the session gains a
   publish state, and the standalone command becomes a thin wrapper over the
   same package.

All four are done. Steps 3 and 4 were built on 1 November 2026 as a separate
upload page rather than fields on the review page, with the details filled in
from a preaching programme spreadsheet; see "Where the fields are collected".

Steps 1 and 2 are a separate binary in this repository rather than a separate
project, so that the eventual move into `internal/publish` is a file move rather
than a rewrite.

## What the website needs

A talk is a post of the custom type `resource`, created at `/wp/v2/resource`.
Over REST that type exposes a title, a date, a featured image, and six
taxonomies. Everything else on its edit screen belongs to an Advanced Custom
Fields (ACF) group that the REST API does not expose: the audio, the Bible
passage, the podcast flag, and the rest. The upload therefore depends on that
group being made writable; see "How the audio is attached".

| Field | On the site | Where it comes from | Notes |
| --- | --- | --- | --- |
| Talk title | `title` | Programme, then the upload page | The session's `title` names the service, which is not the same thing. Titles on the site are the talk's own ("The Lord's Prayer: Our Father's protection"), sometimes just the passage ("Romans 5:1-11"). |
| Date | `date_gmt`, and ACF `resource_date` | Session `startedAt`, correctable on the upload page | The edit screen has its own Date field besides the post date; set both to the service date. `resource_date` is a `Ymd` string, such as `20260913`. Send `date_gmt` so the site's zone (`Europe/London`) cannot shift the post date. Existing talks are often dated on the day they were posted. The talk page shows the post date: "Our Father's pardon" has `resource_date` 6 September but was posted, and is shown, on 13 September. Setting the post date to the service is therefore what keeps a late upload correctly dated. A signed-in read returns `resource_date` for every talk, whoever posted it, so a duplicate check can compare service dates. |
| Church | `resource-church` term | The `church` setting, correctable on the upload page | Three terms: Emmanuel Bishopston 128, Emmanuel City Centre 127, Emmanuel Westbury 129. The app's church name already matches the term name exactly. |
| Kind of resource | `resource-type` term | Fixed | Sermon is term 270. The same type also holds 1,235 blog posts, so omitting this puts a talk among the blogs and out of the sermon listing. The edit screen's Resource Type is ACF `resource_type`, which the schema marks required with exactly one term ID, so `270` is sent there as well as in the taxonomy. |
| Speakers | `resource-contributor` terms | Programme, then the upload page | 97 terms; a talk may have several. Picked from the list wherever possible: the site already has "James Maurice" where a file name says "JamesMorris". A visiting speaker needs a new term, so the app must be able to create one, warning of near matches; see "Credentials". |
| Series | `resource-series` term | Programme, else the church's latest talk | 286 terms, many congregation-specific ("The Lord's Prayer (EB)", "Amazing Grace (2 Kings; ECC)"), though the suffix is not used consistently. Optional: some talks have none. |
| Bible passage | ACF `resource_bible_passages`, free text | Programme, then the upload page | The full reference, such as "Matthew 6:13", or several separated by commas. |
| Book of the Bible | `bible_book` term | Derived | 66 terms, one per book, slugs numbered in canonical order (`040-matthew`). Derived from the book named in the passage and shown for confirmation; a passage spanning several books needs a choice. |
| Topic | `resource-topic` term | Not collected | 17 terms, used mainly by blog posts. Leave empty. |
| Featured image | `featured_media` | The series' latest talk, or a link in the programme | Since summer 2026 every talk in a series shares the series' image; older talks have a shared "Website Sermon Tiles" image or none. |
| Audio | ACF `resource_audio`, an attachment ID | `exports/YYYY-MM-DD-Church.mp3` | Uploaded to the media library attached to the talk, then selected in this field. See below. |
| Include in podcast feed | ACF `resource_include_in_podcast_feed`, the string `Yes` or `No` | `publish.podcast`, then the upload page | Sent explicitly, since the field's default may not apply to a post created over REST. |
| Audio info | ACF `resource_audio_info`: `resource_audio_file_size` in bytes, `resource_audio_duration` as `HH:MM:SS` | The export | Filled in by the site from the media file when a talk is saved in the admin; whether a REST save does the same is unknown. The field is writable, so the app sends both values itself. |
| Video | ACF `resource_video_source` (`YouTube`) and `resource_youtube_url` | The channel's YouTube feed, then the upload page | Set on every recent talk. Sent only when there is a video. |
| Subtitle, Summary, Content, Vimeo URL, Sharing | ACF `resource_subtitle`, `resource_summary`, `resource_content`, `resource_vimeo_url`, `resource_sharing` | Nothing | Not used for sermons; left empty. |

### How the audio is attached

The site holds 1,530 audio files, and each of the 40 most recent is a
media-library attachment whose `post` (parent) is a `resource`. No talk seen
carries an audio URL in a field the REST API shows. The talk page is an
Elementor template whose audio player renders the attachment's `source_url`; on
the one talk checked that has no audio attachment, the player is absent.

The edit screen settles how the player finds the file. The Resources field group
has an Audio field that is a media file selector, so the player reads that
field, and the attachment's parent is a side effect of choosing a file through
it. An upload that only attached the MP3 would produce a talk with no player
and, very probably, nothing in the podcast feed.

That field, the Bible passage, and the podcast flag belong to an ACF group the
REST API does not expose: `acf` is empty in every response and schema. There are
three ways to write them, in order of preference.

1. **Turn on "Show in REST API" for the Resources field group.** This is one
   setting for a site administrator: ACF, then Field Groups, then the group that
   holds these fields, then its Group Settings tab. ACF has supported it since
   5.11, and a File field is written as an attachment ID; both are from ACF's own
   documentation. If the group is defined in the theme's or a plugin's code
   rather than in the admin, the setting cannot be changed there and the code
   has to change instead. The group's fields then appear under `acf` in the
   `resource` schema with their names and types, and are written in the same
   request as the title and terms. Nothing is installed. It also makes those
   values publicly readable, which for a published sermon's passage and audio
   is harmless.
2. **A small mu-plugin** registering the fields with `register_rest_field`. The
   effect is the same, but it is code on the site that someone has to keep.
3. **Writing the post meta by some other route.** ACF stores a hidden reference
   to the field's key beside each value (again recalled), and a value written
   without it may not be read reliably. Not recommended.

Option 1 was done on 13 September 2026. The fields' REST names are in the table
above, and the Lord's Prayer talk now reads back `resource_audio: 15945`, its
attachment's ID, which confirms that the Audio field holds the attachment.

Attached media are served from `downloads.emmanuelbristol.org.uk` rather than
`wp-content/uploads`, so an offload plugin copies each upload to separate
storage. The upload request may therefore take longer than the transfer alone,
while the server copies the file on.

### What a service-length file costs

The 40 most recent audio files on the site are between 18 and 50 minutes long
and 8.7 to 41.8 MB. They are talks, not services. The largest was an ordinary weekly
upload, so the site very probably accepts at least 42 MB through the browser,
though the configured limit has not been seen. This app's exports are mono
variable bitrate at about 70 kbit/s (today's 31-minute file is 16.2 MB), so a
75-minute service would be about 40 MB and a sermon alone about 20 MB. Upload
size is unlikely to be what decides between them.

## Defaults

The details are filled in once, the first time the upload page is opened for a
service, from three sources in order:

1. **The preaching programme.** The administrators keep a spreadsheet of the
   talks planned for the term, in SharePoint, synced to the church computer.
   Its row for the day of the service (and, when a day has more than one
   service, the row whose start time is nearest the recording's) gives the
   title, speakers, series, passage, whether the talk goes into the podcast
   feed, and, for the first talk of a new series, a link to the series' image. Names are then given as the site spells them.
2. **The website.** If the programme gives no series, the church's latest talk
   does. The image is the one the series' latest talk uses: every talk in a
   series on this site shares one featured image (series 738 always 16361,
   736 always 15668), so a new image is needed only when a series starts.
3. **YouTube.** The channel's public feed lists its latest fifteen videos,
   live streams included, without an API key. A stream's publication time is
   not reliably on the service day (25 October's was published at 00:45 on the
   26th), so the first video published between three hours before and a day
   and a half after the start of the recording is chosen.

After that the details are the operator's: opening the page again keeps them,
and "Fill in again from the programme" replaces them only when asked.

The website also supplies the *lists*: the speakers and series offered are the
site's real terms, kept on disk and read again hourly, with that church's recent
ones first, so that the operator picks an existing term rather than creating a
near-duplicate by typing.

The Bishopston record, the fifteen talks from 31 May to 13 September 2026,
showed why the speaker is never defaulted from the last talk: repeating the
last speaker would have been wrong eight times in fourteen, and a confidently
wrong name is eventually accepted without being read. The programme, which
names the planned speaker, replaces that guess.

The book of the Bible is not defaulted; it is derived from the passage. A series
does not fix the book: the Lord's Prayer talk filed under Ezekiel is correct,
because that series draws on different books to show different aspects of the
prayer.

## Where the fields are collected

On a separate upload page, not the dock or the review page. The dock shares a
narrow column with other OBS docks and is deliberately almost empty before a
recording starts. The review page loses its title and church boxes, and Upload
MP3 takes the place of Download MP3. Pressing it starts making the MP3 if there
is no current one and opens the upload page at once, so the details are
checked while the MP3 is made. None of the details is in the MP3, so editing
them never makes the export stale, which they would have done as fields of the
review page.

## Storage

The earlier plan here was a generic, declarative field list per church target.
It was not built: there is one site, its fields are known, and three
congregations share it, so the talk's church is chosen from the site's own list
rather than selecting a target. Configuration says where to upload and where
the details come from:

```jsonc
// config.json
"church": "Emmanuel Bishopston",
"publish": {
  "site": "https://www.emmanuelbristol.org.uk",
  "user": "the uploading account",
  "status": "publish",              // or "private" or "draft" for testing
  "podcast": true,
  "resourceType": "Sermon",
  "programme": "C:\\…\\Preaching programme.xlsx",
  "programmeSheet": "",             // the first worksheet if empty
  "youtubeChannel": "UCReU3dVxdLKPuSs59dBgG6Q"
}
```

The application password is in `website-password.txt` beside `config.json`.

Session schema 3 stores the talk by name, as the operator sees it, and the
upload's progress by ID:

```jsonc
// session.json, schema 3
"talk": {"title": "What's wrong with us?", "date": "2026-11-01",
         "church": "Emmanuel Bishopston", "speakers": ["Jim Walford"],
         "newSpeakers": [], "series": "Life Together", "newSeries": false,
         "passage": "Romans 3:9-20", "podcast": true,
         "videoUrl": "https://www.youtube.com/watch?v=FqAJchBCktM", "image": "",
         "source": "Filled in from row 14 of the programme. …"},
"publish": {"status": "completed", "site": "https://www.emmanuelbristol.org.uk",
            "mp3": "exports/2026-11-01-Emmanuel-Bishopston.mp3", "mp3Finished": "…",
            "progress": {"speakers": {}, "series": {}, "postId": 16500,
                         "mediaId": 16501, "finished": true, "link": "…"},
            "startedAt": "…", "endedAt": "…", "error": ""}
```

Names rather than term IDs are stored because the operator edits names and the
site's terms are looked up again, by name, before every upload; a name that is
not on the site stops the upload unless the operator confirmed it as new. City
Centre joins by running Sermon Companion with its own `church`, or by choosing
the church on the upload page.

## Credentials

WordPress application passwords are the right mechanism, and the site's REST
index advertises them. They are a core feature from WordPress 5.6, they are
per-user and individually revocable, they are sent as ordinary HTTP basic
authentication, and WordPress refuses them over plain HTTP. Two consequences
follow.

**They are not stored in `config.json`.** The review page has an "Edit settings
file" link that opens that file in Notepad, in front of whoever is operating the
computer. A secret goes in a separate file, created with restrictive permissions,
never logged, and never returned by the local HTTP API. That last point matters
more than it first appears: the API is loopback-only, but any page inside OBS's
embedded browser can reach `127.0.0.1:8765`, so "local" is not "private".

**They are not narrowly scoped.** An application password carries the full
authority of the WordPress account it belongs to; WordPress cannot restrict one
to "upload media and create one post type". The mitigation is the account, not
the password: an account with the least role that can do the job, ideally one
used for nothing else. Author can upload files, create and publish its own posts,
and assign terms; Editor is needed to touch anyone else's posts.

Author should also be enough to create a new term, which a volunteer needs for a
visiting speaker or a new series. All six taxonomies on `resource` are
non-hierarchical, and for those the REST API allows creating a term to anyone
holding the taxonomy's `assign_terms` capability, which by default maps to
`edit_posts`. This was checked against `create_item_permissions_check` in
WordPress's `WP_REST_Terms_Controller` and the default capabilities in
`WP_Taxonomy`. `manage_categories` is needed only for a hierarchical taxonomy,
or if the plugin that registers these taxonomies set stricter capabilities,
which cannot be seen without signing in. If it did, the remedy is to grant that
one capability to that one user, with `wp user add-cap <user> manage_categories`
or a role editor plugin, rather than promoting the account to Editor. Signed in
on 1 November 2026, the account (user 8, Author) could assign and create terms in
every `resource` taxonomy, so no extra capability is needed. If
the site has a role editor, or will take a small mu-plugin, the capabilities can
be trimmed further. This is worth stating plainly rather than implying the
credential is safer than it is.

The site already has at least five accounts that upload talks. Judging by the
file names, user 5 posts City Centre's, user 6 Westbury's, users 7 and 8
Bishopston's, and user 3 made a bulk upload of older talks in July.

For Bishopston the decision is that one account, used for nothing but uploading
sermons, does all of the uploading and post creation, and the app holds an
application password for it. The account has the Author role. Revoking the
application password cuts the app off without affecting anything else.

**Sign-in depends on a snippet on the site.** Application passwords did not
work at all until a WPCode snippet, run everywhere, was added on 1 November 2026.
It treats `/wp-json/` requests as API requests, because Defender's Session
Protection otherwise settles the current user before WordPress checks the
password, and it keeps generated application passwords alphanumeric, because
Defender's generator adds symbols that WordPress strips from what it shows and
what it is given but not from what it stores. If the snippet is removed or
deactivated, sign-in fails again with `rest_not_logged_in` rather than with any
error about the password. The publisher should therefore treat that reply to an
authenticated request as its own case, and say that the site's sign-in
arrangement has changed, not that the password is wrong.

Windows DPAPI would encrypt the file at rest against another user of the same
machine. It is a reasonable later addition and not a substitute for the above.

## Reliability

A church broadband connection uploading 20 to 40 MB will fail sometimes, so the
upload is designed around failing rather than around succeeding.

- **Three steps, each recorded.** The talk is created first, as a draft with its
  title, terms, and passage, and its post ID is written to the session. The MP3
  is then uploaded with `post` set to that ID, and the media ID is written. The
  last request sets the Audio field and publishes together, so a published talk
  never lacks its audio. A draft left behind by a failure is invisible
  to the public, whereas an attachment uploaded first is a public file with
  nothing pointing at it. A retry reuses whatever IDs are already recorded.
  Today's hand upload happened in this order, post ID before media ID, though
  older talks show both orders.
- **Auditable in the same way as everything else.** Publish state changes append
  an event to `events.jsonl` before the snapshot is replaced, exactly as export
  and segment edits already do.
- **No duplicates.** Every ID the site gives is stored as soon as it is given,
  so pressing Publish again after a failure carries on rather than starting
  again, and a published talk cannot be published twice. Before carrying on,
  the app checks what the earlier attempt made: a talk deleted on the site
  since is created again, and a file uploaded before the MP3 was made again is
  deleted and replaced, which is safe because the talk is still a draft until
  the last step. (`cmd/sermon-publish` instead reports a deleted talk and
  stops.) A talk already filed by hand for the same church and service date
  stops the upload, before anything is created, until the operator confirms
  it. The final request resends every detail with the audio, so a correction
  made between a failed attempt and its retry is what is published.
- **Size and timeout are discovered deliberately.** Neither the maximum upload
  size nor the PHP timeout is reported over REST; both come from the host's
  configuration, and here the offload plugin adds to the time. They are found
  once, on purpose, with a test file, rather than during a service. The existing
  42 MB upload suggests size is not the constraint. Core WordPress has no chunked
  media upload.
- **Uploading never blocks the recording path.** It runs after the export, on a
  finished session, and a failed upload leaves a perfectly good local MP3.

## Testing

- A fake WordPress built on `httptest`, exercising media upload, term lookup,
  post creation, and the failures that matter: 401 from a stripped
  `Authorization` header, 413 from a file over the limit, a timeout part way
  through the body, and a second upload of a session that already has a post ID.
- `--dry-run` printing the exact requests, so a change can be reviewed without a
  live site.
- One real end-to-end run before the button appears in the application at all,
  with "Include in podcast feed" set to no so that nothing reaches podcast apps.
  Check that the published page's player plays and that Audio info was
  generated, then delete the talk and its attachment.

  Done on 1 November 2026 with `cmd/sermon-publish`, as a private talk (ID
  16485) holding a 38-second test tone. The first attempt created the draft and
  uploaded the file, then the final request was refused: ACF checks every `acf`
  object against the whole field group's schema, so the required
  `resource_type` has to be sent with each update, not only when the talk is
  created. With that fixed, rerunning the command resumed at the failed step
  and finished. Read back signed in, every term and field was as intended, the
  final update had left the draft's passage, date, and podcast fields alone,
  the file was served from the offload host, and the talk was invisible to an
  anonymous visitor.

  Repeated the same day from the application's upload page, as private talk
  16487 with file 16488, from a demonstration session filled in from a test
  programme written by openpyxl. Read back signed in, every field was as
  intended, including the series' image found from its latest talk, the file
  given the talk's title through the upload request, and the file renamed
  from the talk's church rather than the session's. The test found one fault,
  since fixed: two fields left in quick succession sent the second save with
  an out-of-date revision, which was refused.

## A product change to be explicit about

`README.md` used to say that nothing is sent anywhere and that no internet
connection is involved. It now says that this holds unless uploading is set
up, and that with it the application talks to that one website and reads the
church channel's public YouTube feed.

## What the site turned out to be

Findings from the anonymous probe of 13 September 2026, supplemented by a few
direct read-only requests the probe does not yet make. The site runs WordPress
7.1 with Elementor, and its REST API is open at `/wp-json/`.

- **Talks are `resource` posts.** The type holds sermons (1,771) and blog posts
  (1,235), told apart by the `resource-type` taxonomy. The built-in `post` type
  is empty. There is no sermon plugin.
- **The filtering is WP Grid Builder, not FacetWP.** The REST index has `wpgb/v1`
  and `wpgb/v2` namespaces, the talk page loads its scripts, and nothing from
  FacetWP appears. Both plugins use the `_facet=value` query form, as recalled
  rather than checked, which is why the address alone suggested the wrong one. Either way the facets sit over ordinary taxonomies,
  and the upload sets the taxonomy.
- **The facet `_resource_bible_book_2_2` filters by congregation.** Its value
  `emmanuel-bishopston` is a term slug in two taxonomies, `resource-church` (term
  128, 557 posts) and `event-site` (term 267, for events). Only the first applies
  to `resource`. The facet was evidently cloned from the Bible book facet and
  re-pointed without being renamed, as guessed.
- **One site, three congregations.** Bishopston, City Centre, and Westbury are
  terms in `resource-church`.
- **Talk permalinks are `/resources/<slug>/`,** not under `/emmanuel-sermons/`,
  which is a listing page.
- **The audio is an ACF file field** in a field group the REST API does not
  expose, and the MP3 is also attached to the talk. The edit screen, rather than
  the probe, established this. See "How the audio is attached".
- **Someone already uploads this app's output by hand.** The newest attachment,
  `2026-09-13-Emmanuel-Bishopston.mp3`, carries "Created by Sermon Companion" in
  its tags and was uploaded at 12:25, and the talk was published at 12:29 on the
  same day. So the upload this design automates is already happening by hand,
  for Bishopston at least, and on this occasion was published within minutes
  rather than left as a draft.

## Running the probe

The probe has answered what it was built for. One more run is worth making,
authenticated as the uploading account and after the Resources field group is
exposed over REST: it reports the account's role and capabilities, and the
saved `11-schema-resource.json` then names the ACF fields and their types. Its
known defects (term lists stop at 100, `-url` takes one address, and attached
media and ACF properties are not summarised) do not affect that run, so they are
left unfixed, and the probe is retired once `cmd/sermon-publish` exists.

```sh
go run ./cmd/wp-probe \
  -site https://www.emmanuelbristol.org.uk \
  -url  'https://www.emmanuelbristol.org.uk/resources/the-lords-prayer-our-fathers-protection/' \
  -user '<account username>' \
  -password-file '<file holding the application password>' \
  -out  work/wp-probe-auth
```

The password is read from a file, or from `WP_APP_PASSWORD`, so that it stays
out of the shell history and the process list. The command makes only `GET` and
`OPTIONS` requests and keeps the account's email address out of what it saves.

## Open questions

None of these blocks step 2.

1. **Is Audio info generated when a talk is saved over REST?** No longer
   matters: the app sends the size and duration, and the test talk kept them.
2. **Which date does the listing show?** Answered: the post date, so the app
   sets it to the service date as well as `resource_date`.
3. **What is the site's maximum upload size?** Media then Add New in the admin,
   "Maximum upload file size". The 42 MB file already on the site suggests it is
   no constraint.
4. **Will City Centre use it too?** Its computer needs only its own `church`
   and `publish` settings and, if it keeps its own uploading account, its own
   application password. A shared programme spreadsheet can give each row a
   Church.
5. **Is the programme's real layout as assumed?** The reader finds columns by
   heading and was tested with a workbook written by openpyxl, not yet with one
   saved by the administrators' Excel from SharePoint.

Settled on 13 September 2026: the MP3 is the sermon, with the reading and Q&A
when they are marked, never the whole service; talks are published directly
rather than drafted; uploads are weekly, by different members of the A/V team;
one Author account, used for nothing else, does all of Bishopston's uploading
and post creation; and the session's Church field chooses the congregation. Settled on 1 November
2026: application passwords work, through the WPCode snippet described under
"Credentials", and the Author account can create the terms it needs.
