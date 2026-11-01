# Automatic sermon upload: working design

This is a working note, not a settled specification. It records what uploading a
finished MP3 to a church website needs, what is already known, and what has to be
answered before any of it is built. The Emmanuel Bristol site was probed
anonymously on 13 September 2026; what that established, and what it could not,
is set out under "What the site turned out to be" and in the open questions at
the end.

## Goal and deliberate non-goals

After a service, the operator presses one button on the review page and the MP3,
together with its title, speaker, series, and Bible passage, arrives on the
church website as a published sermon. Everything the website needs is collected
during or shortly after the service, mostly as defaults the operator confirms
rather than as boxes to fill in. Nobody has to log in to the website.

The MP3 is the existing export of the segments the operator included: the
sermon, with the reading and Q&A when they are marked. The whole service is
never uploaded, and the export path needs no change for this.

A talk is uploaded every week, but by different members of the A/V team, each of
whom may do it only once every month or two. The design is judged by whether someone who has forgotten how it
works still gets it right, which rules out a draft that someone must then
publish on the site: logging in is the step being removed. The check a draft
would have provided moves into the app, as a final summary of exactly what will
be published (title, speaker, series, passage, church, and the MP3's length)
shown when the button is pressed. Publishing is not cheap to undo. The podcast
flag is on by default, and the site links to Apple Podcasts and Spotify shows
for each congregation, so a published talk very probably reaches podcast apps,
where a correction on the site does not follow it quickly. How those feeds are
produced has not been checked.

Not in scope for now:

- Building a podcast feed, social posts, or transcripts. The website is one
  integration; building a general publishing framework before a single site works
  is the wrong order.
- Uploading the lossless source. The MP3 is the published artefact.
- Setting the YouTube link a talk page shows. It lives in a field the REST API
  does not expose (see below) and belongs to whoever publishes the video.

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

| Field | On the site | Where it comes from today | Notes |
| --- | --- | --- | --- |
| Talk title | `title` | Not collected | The session's `title` names the service, which is not the same thing. Titles on the site are the talk's own ("The Lord's Prayer: Our Father's protection"), sometimes just the passage ("Romans 5:1-11"). |
| Date | `date_gmt`, and ACF `resource_date` | Session `startedAt` | The edit screen has its own Date field besides the post date; set both to the service date. `resource_date` is a `Ymd` string, such as `20260913`. Send `date_gmt` so the site's zone (`Europe/London`) cannot shift the post date. Existing talks are often dated on the day they were posted. The talk page shows the post date: "Our Father's pardon" has `resource_date` 6 September but was posted, and is shown, on 13 September. Setting the post date to the service is therefore what keeps a late upload correctly dated. A signed-in read returns `resource_date` for every talk, whoever posted it, so a duplicate check can compare service dates. |
| Church | `resource-church` term | Session `church` | Three terms: Emmanuel Bishopston 128, Emmanuel City Centre 127, Emmanuel Westbury 129. The app's church name already matches the term name exactly. |
| Kind of resource | `resource-type` term | Fixed | Sermon is term 270. The same type also holds 1,235 blog posts, so omitting this puts a talk among the blogs and out of the sermon listing. The edit screen's Resource Type is ACF `resource_type`, which the schema marks required with exactly one term ID, so `270` is sent there as well as in the taxonomy. |
| Speaker | `resource-contributor` term | Not collected | 97 terms. Picked from the list wherever possible: the site already has "James Maurice" where a file name says "JamesMorris". A visiting speaker needs a new term, so the app must be able to create one, warning of near matches; see "Credentials". |
| Series | `resource-series` term | Not collected | 286 terms, many congregation-specific ("The Lord's Prayer (EB)", "Amazing Grace (2 Kings; ECC)"), though the suffix is not used consistently. Optional: some talks have none. |
| Bible passage | ACF `resource_bible_passages`, free text | Not collected | The full reference, such as "Matthew 6:13". Typed each time. |
| Book of the Bible | `bible_book` term | Derived | 66 terms, one per book, slugs numbered in canonical order (`040-matthew`). Derived from the book named in the passage and shown for confirmation; a passage spanning several books needs a choice. |
| Topic | `resource-topic` term | Not collected | 17 terms, used mainly by blog posts. Leave empty. |
| Featured image | `featured_media` | Not collected | Set on some talks (a shared "Website Sermon Tiles" image), not on others. Leave empty unless the church wants a default. |
| Audio | ACF `resource_audio`, an attachment ID | `exports/YYYY-MM-DD-Church.mp3` | Uploaded to the media library attached to the talk, then selected in this field. See below. |
| Include in podcast feed | ACF `resource_include_in_podcast_feed`, the string `Yes` or `No` | Fixed | Sent as `Yes` explicitly, since the field's default may not apply to a post created over REST. |
| Audio info | ACF `resource_audio_info`: `resource_audio_file_size` in bytes, `resource_audio_duration` as `HH:MM:SS` | The export | Filled in by the site from the media file when a talk is saved in the admin; whether a REST save does the same is unknown. The field is writable, so the app sends both values itself. |
| Subtitle, Summary, Content, Video source, YouTube and Vimeo URLs, Sharing | ACF `resource_subtitle`, `resource_summary`, `resource_content`, `resource_video_source`, `resource_youtube_url`, `resource_vimeo_url`, `resource_sharing` | Nothing | Not used for sermons; left empty. |

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

## Defaults from the last talk

The rule asked for is that a new service borrows its speaker, series, and the
rest from the last talk for the same church. Two sources are possible and they
are not equivalent.

- **The last local session with the same church.** Works offline, is instant, and
  is available at the moment of recording. It goes blank once retention has
  deleted the old sessions, which by default is after 60 days.
- **The last published talk on the website for that church.** Survives retention
  and reflects what was actually published, including any correction made on the
  site. It needs the network, and it needs the site to be reachable at the moment
  the default is wanted. On this site it is one request:
  `/wp/v2/resource?resource-church=128&resource-type=270&_embed=wp:term`.

The website is the primary source. Uploads are weekly, so last week's session
will normally still be on the church computer, but they are made by different
people, and the website is what reflects any correction made to a talk after it
was uploaded, whoever uploaded it and from wherever. The website also supplies the *lists*: the speakers and series offered in
the interface are the site's real terms, cached, so that the operator picks an
existing term rather than creating a near-duplicate by typing. The last local
session for the same church is the fallback when the site cannot be reached.

The Bishopston record, the fifteen talks from 31 May to 13 September 2026,
settles two of these. Series run for several weeks (1 Thessalonians for at least
eight talks, then 2 Kings for three, then the Lord's Prayer for four so far), so
the series defaults unconditionally.

The speaker should not default at all. An earlier draft of this note proposed
defaulting it only when the last two talks for that church had the same speaker.
Applied to that record, the rule would have offered a name four times and been
wrong twice; simply repeating the last speaker would have been wrong eight times
in fourteen. A confidently wrong name is worse than an empty box, because it is
eventually accepted without being read. Instead the list puts that church's
recent speakers first, which makes the right choice one click without ever
making it for the operator.

The book of the Bible is not defaulted; it is derived from the passage, which is
typed each time. A series does not fix the book: the Lord's Prayer talk filed
under Ezekiel is correct, because that series draws on different books to show
different aspects of the prayer.

The series list needs narrowing to be usable: 286 terms with no church attached
to them. Offer first the series used by that church's recent talks, then the
rest.

## Where the fields are collected

On the review page, not the dock. The dock shares a narrow column with other OBS
docks and is deliberately almost empty before a recording starts; adding metadata
fields to it would cost vertical space during the one moment the operator has
something else to do. The review page already edits the title and the church, and
saves as it goes, so the new fields belong beside them.

## Storage

The field set is site-specific, which is why the probe exists. The session model
should therefore stay generic in the way the segment model already is:
configuration declares which fields to collect, and the session stores their
values. Emmanuel Bristol is one site with three congregations, so the common case
is several church names pointing at one site, each pinning its own congregation
term.

```jsonc
// config.json
"publish": {
  "targets": {
    "Emmanuel Bishopston": {
      "site": "https://www.emmanuelbristol.org.uk",
      "user": "sermon-companion",
      "postType": "resource",
      "status": "publish",
      "fixedTerms": {"resource-church": 128, "resource-type": 270},
      "fixedAcf": {"resource_type": 270, "resource_include_in_podcast_feed": "Yes"},
      "fields": [
        {"key": "speaker", "label": "Speaker", "source": {"taxonomy": "resource-contributor"}, "carryOver": "never", "recentFirst": true},
        {"key": "series",  "label": "Series",  "source": {"taxonomy": "resource-series"}, "carryOver": "always", "optional": true},
        {"key": "passage", "label": "Bible passage", "source": {"acf": "resource_bible_passages"}, "carryOver": "never"},
        {"key": "book",    "label": "Book of the Bible", "source": {"taxonomy": "bible_book"}, "derivedFrom": "passage"}
      ]
    }
  }
}
```

```jsonc
// session.json, schema 3
"talk": {"title": "The Lord's Prayer: Our Father's protection",
         "fields": {"speaker": 296, "series": 736, "passage": "Matthew 6:13", "book": 239}},
"publish": {"status": "uploaded", "target": "Emmanuel Bishopston", "postId": 15944,
            "mediaId": 15945, "postUrl": "https://www.emmanuelbristol.org.uk/?p=15944",
            "startedAt": "...", "endedAt": "...", "error": ""}
```

Term fields store the term ID, which is what the site accepts and survives a
rename; the interface shows the cached name. Keying the targets by church makes
"the same church" mean something concrete and still handles a church with a site
of its own. The session's existing Church field selects the target, so City
Centre joins by adding a target that pins term 127. The schema change is a minor version bump under the project's own
rules.

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
- **No duplicates.** The created post ID is stored. A second press of the button
  updates that post rather than creating a second one, and if the stored post has
  been deleted on the site, that is reported rather than silently recreated.
  Replacing the audio uploads a new attachment and detaches the old one; it does
  not delete it.
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

## A product change to be explicit about

`README.md` currently says, in as many words, that nothing is sent anywhere and
that no internet connection is involved. That stops being true the moment this
feature exists. The claim should change to something accurate and narrower: the
recording and review remain entirely local, and the only thing ever sent is a
finished MP3 and its details, to a site the operator has configured, when the
operator asks for it.

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
4. **Will City Centre use it too?** The session's Church field selects the
   target, so another congregation needs only a configuration entry and, if it
   keeps its own uploading account, its own application password.

Settled on 13 September 2026: the MP3 is the sermon, with the reading and Q&A
when they are marked, never the whole service; talks are published directly
rather than drafted; uploads are weekly, by different members of the A/V team;
one Author account, used for nothing else, does all of Bishopston's uploading
and post creation; and the session's Church field chooses the congregation. Settled on 1 November
2026: application passwords work, through the WPCode snippet described under
"Credentials", and the Author account can create the terms it needs.
