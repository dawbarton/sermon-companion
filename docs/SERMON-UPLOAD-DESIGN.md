# Automatic sermon upload: working design

This is a working note, not a settled specification. It records what uploading a
finished MP3 to a church website needs, what is already known, and what has to be
answered before any of it is built. The Emmanuel Bristol site was probed
anonymously on 13 September 2026; what that established, and what it could not,
is set out under "What the site turned out to be" and in the open questions at
the end.

## Goal and deliberate non-goals

After a service, the operator presses one button on the review page and the MP3,
together with its title, speaker, series, and book of the Bible, arrives on the
church website as a sermon entry. Everything the website needs is collected
during or shortly after the service, mostly as defaults the operator confirms
rather than as boxes to fill in.

Not in scope for now:

- Publishing live without a person looking. The default is a draft that someone
  presses publish on. Wrong audio on a public page is far more expensive than one
  extra click, and the failure mode is embarrassing rather than merely annoying.
  The site's talk pages link to Apple Podcasts and Spotify shows for each
  congregation, so a published talk very probably reaches podcast apps as well,
  where a correction on the site does not follow it quickly. How those feeds are
  produced has not been checked.
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
That type supports a title, a date, a featured image, and six taxonomies, and
nothing else: it has no body, no excerpt, no author field over REST, and no
registered meta. The fields below are therefore the whole of what an upload can
set.

| Field | On the site | Where it comes from today | Notes |
| --- | --- | --- | --- |
| Talk title | `title` | Not collected | The session's `title` names the service, which is not the same thing. Titles on the site are the talk's own ("The Lord's Prayer: Our Father's protection"), sometimes just the passage ("Romans 5:1-11"). |
| Date | `date_gmt` | Session `startedAt` | Send `date_gmt` so the site's zone (`Europe/London`) cannot shift it. Existing talks are dated inconsistently, often on the day they were posted rather than the day they were preached; using the service date is a deliberate choice and makes the listing order right. |
| Church | `resource-church` term | Session `church` | Three terms: Emmanuel Bishopston 128, Emmanuel City Centre 127, Emmanuel Westbury 129. The app's church name already matches the term name exactly. |
| Kind of resource | `resource-type` term | Fixed | Sermon is term 270. The same type also holds 1,235 blog posts, so omitting this puts a talk among the blogs and out of the sermon listing. |
| Speaker | `resource-contributor` term | Not collected | 97 terms. Picked from the list, never typed: the site already has "James Maurice" where a file name says "JamesMorris". |
| Series | `resource-series` term | Not collected | 286 terms, many congregation-specific ("The Lord's Prayer (EB)", "Amazing Grace (2 Kings; ECC)"), though the suffix is not used consistently. Optional: some talks have none. |
| Book of the Bible | `bible_book` term | Not collected | 66 terms, one per book, slugs numbered in canonical order (`040-matthew`). A book, not a passage. There is no passage field; where a passage appears, it is in the title. |
| Topic | `resource-topic` term | Not collected | 17 terms, used mainly by blog posts. Leave empty. |
| Featured image | `featured_media` | Not collected | Set on some talks (a shared "Website Sermon Tiles" image), not on others. Leave empty unless the church wants a default. |
| The MP3 | A media attachment whose parent is the talk | `exports/YYYY-MM-DD-Church.mp3` | See below. |

### How the audio is attached

The site holds 1,530 audio files, and each of the 40 most recent is a
media-library attachment whose `post` (parent) is a `resource`. No talk seen
carries an audio URL in a field the REST API shows. The talk page is an
Elementor template whose audio player renders the attachment's `source_url`; on
the one talk checked that has no audio attachment, the player is absent.

What the probe cannot see is how the player finds the file. Two mechanisms fit
the evidence equally well: the template reads the talk's attached audio, or it
reads an ACF file field whose uploads WordPress also attaches to the post. The
second is plausible, because the same template shows a per-talk YouTube link
that must come from a custom field, and `acf` is empty in every REST response
and schema, which is what a field group not exposed to REST looks like. If the
player reads a hidden field, an upload that only attaches the file produces a
talk with no player. This is settled by looking at a talk's edit screen in the
WordPress admin, or by one test draft, before step 2 is written.

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

The proposal is to use the local session as the primary source, fall back to the
website when there is no local history, and use the website in both cases for the
*lists*: the set of speakers and series offered in the interface should be the
site's real terms, cached, so that the operator picks an existing term rather
than creating a near-duplicate by typing.

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

The book of the Bible usually follows the series but not always (one talk in the
Lord's Prayer series is filed under Ezekiel), so it defaults with the series and
is shown for confirmation.

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
      "status": "draft",
      "fixedTerms": {"resource-church": 128, "resource-type": 270},
      "fields": [
        {"key": "speaker", "label": "Speaker", "source": {"taxonomy": "resource-contributor"}, "carryOver": "never", "recentFirst": true},
        {"key": "series",  "label": "Series",  "source": {"taxonomy": "resource-series"}, "carryOver": "always", "optional": true},
        {"key": "book",    "label": "Book of the Bible", "source": {"taxonomy": "bible_book"}, "carryOver": "withSeries"}
      ]
    }
  }
}
```

```jsonc
// session.json, schema 3
"talk": {"title": "The Lord's Prayer: Our Father's protection",
         "fields": {"speaker": 296, "series": 736, "book": 239}},
"publish": {"status": "uploaded", "target": "Emmanuel Bishopston", "postId": 15944,
            "mediaId": 15945, "postUrl": "https://www.emmanuelbristol.org.uk/?p=15944",
            "startedAt": "...", "endedAt": "...", "error": ""}
```

Term fields store the term ID, which is what the site accepts and survives a
rename; the interface shows the cached name. Keying the targets by church makes
"the same church" mean something concrete and still handles a church with a site
of its own. The schema change is a minor version bump under the project's own
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
the password: a dedicated user, created for this purpose, with the least role
that can do the job. Author can upload files, create and publish its own posts,
and assign existing terms; Editor is needed to touch anyone else's posts. If the
site keeps WordPress's default capabilities for its custom taxonomies, creating
a *new* term needs `manage_categories`, which Author lacks, so a new speaker or
series would have to be added on the site first. That is arguably the right
behaviour, since it prevents near-duplicates, but it has to be said in the
interface. The capability mapping for these taxonomies has not been checked. If
the site has a role editor, or will take a small mu-plugin, the capabilities can
be trimmed further. This is worth stating plainly rather than implying the
credential is safer than it is.

The site already has at least five accounts that upload talks. Judging by the
file names, user 5 posts City Centre's, user 6 Westbury's, users 7 and 8
Bishopston's, and user 3 made a bulk upload of older talks in July. A
dedicated account keeps the app's uploads distinguishable from theirs.

Windows DPAPI would encrypt the file at rest against another user of the same
machine. It is a reasonable later addition and not a substitute for the above.

## Reliability

A church broadband connection uploading 20 to 40 MB will fail sometimes, so the
upload is designed around failing rather than around succeeding.

- **Three steps, each recorded.** The talk is created first, as a draft, and its
  post ID is written to the session. The MP3 is then uploaded with `post` set to
  that ID, and the media ID is written. Only then is the talk published, if
  publishing is configured at all. A draft left behind by a failure is invisible
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
- One real end-to-end run into a draft, deleted afterwards, before the button
  appears in the application at all. Check the draft's preview shows the audio
  player; that is the test of the question under "How the audio is attached".

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
- **The audio is an attachment, very probably displayed through it.** See "How
  the audio is attached", including the one thing still unconfirmed.
- **Someone already uploads this app's output by hand.** The newest attachment,
  `2026-09-13-Emmanuel-Bishopston.mp3`, carries "Created by Sermon Companion" in
  its tags and was uploaded at 12:25, and the talk was published at 12:29 on the
  same day. So the upload this design automates is already happening by hand,
  for Bishopston at least, and on this occasion was published within minutes
  rather than left as a draft.

## Running the probe

```sh
go run ./cmd/wp-probe \
  -site https://www.emmanuelbristol.org.uk \
  -url  'https://www.emmanuelbristol.org.uk/resources/the-lords-prayer-our-fathers-protection/' \
  -out  work/wp-probe
```

`-url` currently takes one address. A talk's permalink resolves the post type
and fields; a filtered listing resolves the facet parameters. Until the flag can
be repeated, run it once with each to get both.

Credentials are optional and most of the above is publicly readable without
them. To see what an account may write, add `-user` and supply the application
password through `WP_APP_PASSWORD` or `-password-file`, so that it stays out of
the shell history and the process list. The command makes only `GET` and
`OPTIONS` requests, writes `work/wp-probe/SUMMARY.md` and the raw responses
beside it, and keeps the account's email address out of both.

## Open questions

The first blocks step 2; the next two change what the app produces.

1. **Does the audio player read the attachment or a hidden field?** A talk's edit
   screen in the WordPress admin answers it: an audio or file field there means a
   hidden ACF field, and its absence means the attachment. If it is a field, the
   options are asking the site's administrator to expose that field group to the
   REST API, or setting it some other way.
2. **Whole service or sermon only?** Every talk on the site is 18 to 50 minutes,
   so the site carries talks rather than services, and today's hand upload of
   this app's export was 31 minutes. What remains is whether that export was the
   sermon alone or included the reading, and whether the app should produce a
   sermon-only file of its own. The second reaches back into mastering, not just
   the upload.
3. **Draft or published?** The recommendation remains draft by default. Today's
   hand upload was published within minutes, and publishing probably reaches podcast
   apps, which cuts both ways: it is the reason for caution, and it is the reason
   someone may want no further step.
4. **Who posts these now, and will the app take over or share?** Several accounts
   upload talks, apparently one per congregation, and one of them is already
   uploading this app's MP3 for Bishopston. If the app uploads while a person
   also posts by hand, the site gets two talks. The app should probably serve
   only the congregations whose uploader has agreed to stop doing it by hand.
5. **Are there churches outside Emmanuel Bristol?** If not, the per-church target
   map is simply three names pointing at one site.
6. **What is the site's maximum upload size?** Visible in the WordPress admin at
   Media then Add New, stated as "Maximum upload file size". The 42 MB file
   already on the site suggests it is no constraint; the figure confirms it.
7. **What role will the dedicated account have, and can it create terms?** Answered
   by the authenticated probe once an account exists.
