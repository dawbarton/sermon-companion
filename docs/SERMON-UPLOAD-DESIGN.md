# Automatic sermon upload: working design

This is a working note, not a settled specification. It records what uploading a
finished MP3 to a church website needs, what is already known, and what has to be
answered before any of it is built. It will be wrong in places until the site has
actually been examined; the open questions at the end say which places.

## Goal and deliberate non-goals

After a service, the operator presses one button on the review page and the MP3,
together with its title, speaker, series, and passage, arrives on the church
website as a sermon entry. Everything the website needs is collected during or
shortly after the service, mostly as defaults the operator confirms rather than
as boxes to fill in.

Not in scope for now:

- Publishing live without a person looking. The default is a draft that someone
  presses publish on. Wrong audio on a public page is far more expensive than one
  extra click, and the failure mode is embarrassing rather than merely annoying.
- A podcast feed, social posts, or transcripts. The website is one integration;
  building a general publishing framework before a single site works is the wrong
  order.
- Uploading the lossless source. The MP3 is the published artefact.

## Build order

The website interface is the uncertain part, and the recording path is the part
that must not break. They are therefore separated for as long as possible.

1. **Reconnaissance.** `cmd/wp-probe` reads the site's REST API and reports the
   post type talks live in, the taxonomies holding a speaker or a series, the
   fields the site accepts, and what a given account may write. It makes only
   read requests. This exists now.
2. **A standalone publisher.** `cmd/sermon-publish` takes an existing session
   directory and its exported MP3 and performs the upload, with `--dry-run`
   printing the exact requests it would make. It reads `session.json` through
   `internal/store` and writes nothing back. The main application is untouched,
   so the whole website interface can be got wrong repeatedly on a weekday
   without any risk to Sunday.
3. **Metadata collection in the app.** Only once the field set is known from
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

The exact list comes from the probe. The following is what sermon plugins
generally want, and is the checklist to confirm against the real site.

| Field | Where it comes from today | Notes |
| --- | --- | --- |
| Talk title | Not collected | The session's `title` names the service, which is not the same thing. A talk needs its own title. |
| Date | Session `startedAt` | Already exact. Watch the site's timezone: a date sent without a zone is read in the site's, not the computer's. |
| Church | Session `church` | Already editable per service. |
| Speaker | Not collected | Almost certainly a taxonomy term on the site, so it should be a list rather than free text. |
| Series | Not collected | Likewise a taxonomy term, and the field most likely to carry over from the previous talk. |
| Bible passage | Not collected | Free text on most plugins. |
| Service or time of day | Not collected | Some plugins have a "service type" taxonomy. Confirm. |
| Description | Not collected | Optional on every plugin seen. |
| Duration | Derivable from the export | Some plugins store it; most read it from the file. |
| The MP3 | `exports/YYYY-MM-DD-Church.mp3` | Uploaded as an attachment, then linked from the talk. |

The uploaded file is currently the whole service export, reading and Q&A
included. Whether the website should instead receive a sermon-only file is an
open question below, and it is the one that most affects the export path.

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
  the default is wanted.

The proposal is to use the local session as the primary source, fall back to the
website when there is no local history, and use the website in both cases for the
*lists*: the set of speakers and series offered in the interface should be the
site's real terms, cached, so that the operator picks an existing term rather
than creating a near-duplicate by typing.

One refinement worth considering: default the series unconditionally, because a
series continues, but default the speaker only when the last two talks for that
church had the same speaker. A church with a rotating preaching team would
otherwise be handed a confidently wrong name every week, which is worse than an
empty box because it will eventually be accepted without being read.

## Where the fields are collected

On the review page, not the dock. The dock shares a narrow column with other OBS
docks and is deliberately almost empty before a recording starts; adding metadata
fields to it would cost vertical space during the one moment the operator has
something else to do. The review page already edits the title and the church, and
saves as it goes, so the new fields belong beside them.

## Storage

The field set is site-specific, which is exactly why the probe exists. The
session model should therefore stay generic in the way the segment model already
is: configuration declares which fields to collect, and the session stores their
values.

```jsonc
// config.json
"publish": {
  "targets": {
    "St Mary's Church": {
      "site": "https://example.org",
      "user": "sermon-companion",
      "postType": "wpfc_sermon",
      "status": "draft",
      "fields": [
        {"key": "speaker", "label": "Speaker", "source": {"taxonomy": "wpfc_preacher"}, "carryOver": "whenStable"},
        {"key": "series",  "label": "Series",  "source": {"taxonomy": "wpfc_sermon_series"}, "carryOver": "always"},
        {"key": "passage", "label": "Bible passage", "carryOver": "never"}
      ]
    }
  }
}
```

```jsonc
// session.json, schema 3
"talk": {"title": "The Prodigal Son", "fields": {"speaker": "David Barton", "series": "Luke"}},
"publish": {"status": "uploaded", "target": "St Mary's Church", "mediaId": 913,
            "postId": 4021, "postUrl": "https://example.org/?p=4021",
            "startedAt": "...", "endedAt": "...", "error": ""}
```

Keying the targets by church makes "the same church" mean something concrete and
handles the case of several churches with several sites. The schema change is a
minor version bump under the project's own rules.

## Credentials

WordPress application passwords are the right mechanism. They are a core feature
from WordPress 5.6, they are per-user and individually revocable, they are sent
as ordinary HTTP basic authentication, and WordPress refuses them over plain
HTTP. Two consequences follow.

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
that can do the job. Author can publish its own posts; Editor is needed to touch
anyone else's. If the site has a role editor, or will take a small mu-plugin, the
capabilities can be trimmed further. This is worth stating plainly rather than
implying the credential is safer than it is.

Windows DPAPI would encrypt the file at rest against another user of the same
machine. It is a reasonable later addition and not a substitute for the above.

## Reliability

A church broadband connection uploading 30 to 60 MB will fail sometimes, so the
upload is designed around failing rather than around succeeding.

- **Two phases, separately recorded.** The attachment is uploaded first and its
  media ID is written to the session before the talk is created. A retry after a
  failure between the two reuses the attachment rather than uploading the file
  again, and no orphan is left behind unnoticed.
- **Auditable in the same way as everything else.** Publish state changes append
  an event to `events.jsonl` before the snapshot is replaced, exactly as export
  and segment edits already do.
- **No duplicates.** The created post ID is stored. A second press of the button
  updates that post rather than creating a second one, and if the stored post has
  been deleted on the site, that is reported rather than silently recreated.
- **Size and timeout are discovered deliberately.** Neither the maximum upload
  size nor the PHP timeout is reported over REST; both come from the host's
  configuration. They are found once, on purpose, with a test file, rather than
  during a service. If the limit is below a service-length MP3, the options are
  raising it on the host, uploading by SFTP and registering the attachment, or a
  plugin that accepts chunks. Core WordPress has no chunked media upload.
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
  appears in the application at all.

## A product change to be explicit about

`README.md` currently says, in as many words, that nothing is sent anywhere and
that no internet connection is involved. That stops being true the moment this
feature exists. The claim should change to something accurate and narrower: the
recording and review remain entirely local, and the only thing ever sent is a
finished MP3 and its details, to a site the operator has configured, when the
operator asks for it.

## Open questions

These change the design rather than the wording, and the first two block step 2.

1. **Which site, and what is a sermon on it?** One URL is enough to answer most
   of this, without credentials. Is it a sermon plugin with its own post type and
   taxonomies, or ordinary posts in a category with the MP3 linked by hand?
2. **One church or several, and one site or several?** The `church` field being
   per-session, and the request for defaults from the last talk "for the same
   church", suggests more than one. If several churches share one site, is there
   a church taxonomy, or does the site distinguish them another way?
3. **Whole service or sermon only?** The export today is one MP3 of everything
   marked. If the website should carry only the sermon, the mastering path needs
   a second, sermon-only output, which is a bigger change than the upload itself.
4. **Draft or published?** The recommendation is draft by default. Say if the
   point of the exercise is that nobody has to touch the website afterwards.
5. **Who else publishes there?** If someone currently creates these posts by
   hand, an automatic upload that fights them is worse than no upload. Knowing
   the existing habit shapes whether the app creates a post or only an
   attachment.
6. **What is the site's maximum upload size?** Visible in the WordPress admin at
   Media then Add New, stated as "Maximum upload file size". If it is below about
   60 MB, question 3 becomes more pressing.
