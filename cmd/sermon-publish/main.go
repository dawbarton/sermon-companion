// Command sermon-publish uploads a finished Sermon Companion session to the
// church website as a talk: the MP3, its title, speaker, series, and Bible
// passage. It is the standalone stage of automatic upload described in
// docs/SERMON-UPLOAD-DESIGN.md. It reads a session folder and a small file of
// talk details, and never writes into the session, so that the website side can
// be got wrong repeatedly on a weekday without any risk to Sunday.
//
// Usage:
//
//	go run ./cmd/sermon-publish \
//	  -site https://www.emmanuelbristol.org.uk \
//	  -user uploads@example.org -password-file work/app-password.txt \
//	  -session "$HOME/Library/Application Support/Sermon Companion/sessions/<id>" \
//	  -talk work/talk.json -status private -podcast=false
//
// The talk file holds what only the operator knows:
//
//	{
//	  "title": "The Lord's Prayer: Our Father's protection",
//	  "speaker": "Jim Walford",
//	  "series": "The Lord's Prayer (EB)",
//	  "passage": "Matthew 6:13, Ezekiel 36:22-27"
//	}
//
// A speaker or series not yet on the site needs "newSpeaker": true or
// "newSeries": true, so that a typing slip cannot add a duplicate.
//
// -dry-run prints the requests without making them. Otherwise the command asks
// for "yes" before changing anything, unless -yes is given. Every ID the site
// gives is recorded under -state-dir as soon as it is given, so running the
// same command again after a failure carries on from where it stopped, and
// running it after a success reports the talk instead of uploading it twice.
// -delete removes what the recorded upload created, after asking for "delete".
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/dawbarton/sermon-companion/internal/publish"
)

// environment is what the command uses from outside itself, so that tests can
// give it a fake site, a fixed duration, and typed answers.
type environment struct {
	in    io.Reader
	out   io.Writer
	http  *http.Client
	probe func(ctx context.Context, ffprobe, path string) (time.Duration, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	env := environment{in: os.Stdin, out: os.Stdout, http: &http.Client{}, probe: publish.ProbeDuration}
	if err := run(ctx, os.Args[1:], env); err != nil {
		fmt.Fprintln(os.Stderr, "sermon-publish:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, env environment) error {
	flags := flag.NewFlagSet("sermon-publish", flag.ContinueOnError)
	flags.SetOutput(env.out)
	site := flags.String("site", "", "address of the WordPress site, starting https://")
	user := flags.String("user", "", "WordPress user name or email address the application password belongs to")
	passwordFile := flags.String("password-file", "", "file holding the application password")
	sessionDir := flags.String("session", "", "the session folder to upload from")
	talkFile := flags.String("talk", "", "JSON file of the talk's title, speaker, series, and passage")
	status := flags.String("status", "", "publish, private, or draft; there is deliberately no default")
	podcast := flags.Bool("podcast", true, "include the talk in the site's podcast feed")
	ffprobe := flags.String("ffprobe", "ffprobe", "FFprobe program, used to read the MP3's duration")
	stateDir := flags.String("state-dir", filepath.Join("work", "publish-state"), "folder for the records that let a failed upload resume")
	timeout := flags.Duration("timeout", 30*time.Minute, "time allowed for the whole upload")
	dryRun := flags.Bool("dry-run", false, "print the requests instead of making them")
	yes := flags.Bool("yes", false, "upload without asking first")
	remove := flags.Bool("delete", false, "delete what the recorded upload for this session created")
	if err := flags.Parse(args); err != nil {
		return err
	}
	required := map[string]string{"site": *site, "user": *user, "password-file": *passwordFile, "session": *sessionDir}
	if !*remove {
		required["talk"], required["status"] = *talkFile, *status
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("-%s is required", name)
		}
	}
	if *remove && *dryRun {
		return errors.New("-delete and -dry-run cannot be used together")
	}

	password, err := os.ReadFile(*passwordFile)
	if err != nil {
		return fmt.Errorf("could not read the password file: %w", err)
	}
	export, err := publish.LoadExport(*sessionDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, err := publish.NewClient(*site, *user, strings.TrimSpace(string(password)), env.http)
	if err != nil {
		return err
	}
	account, err := client.Me(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.out, "Signed in as %s (%s).\n", account.Slug, strings.Join(account.Roles, ", "))

	path := recordPath(*stateDir, export.Session.ID)
	rec, recorded, err := loadRecord(path)
	if err != nil {
		return err
	}
	answers := bufio.NewScanner(env.in)
	if *remove {
		return removeUpload(ctx, env, answers, client, rec, recorded, path)
	}

	talk, err := readTalk(*talkFile)
	if err != nil {
		return err
	}
	if !account.Capabilities["upload_files"] {
		return errors.New("the account may not upload files")
	}
	if *status == "publish" && !account.Capabilities["publish_posts"] {
		return errors.New("the account may not publish; use -status draft or give it the Author role")
	}
	if recorded && rec.Progress.Finished {
		fmt.Fprintf(env.out, "This session has already been uploaded: %s\nNothing was sent. The record is %s.\n", rec.Progress.Link, path)
		return nil
	}
	duration, err := env.probe(ctx, *ffprobe, export.Path)
	if err != nil {
		return err
	}
	plan, warnings, err := publish.Prepare(ctx, client, export, talk, publish.Options{Status: *status, Podcast: *podcast}, duration)
	if err != nil {
		return err
	}
	describe(env.out, plan)
	for _, warning := range warnings {
		fmt.Fprintln(env.out, "Warning:", warning)
	}

	if *dryRun {
		fmt.Fprintln(env.out, "\nThe requests below would be made in this order. Negative IDs stand for ones the site would give.")
		return publish.Run(ctx, &publish.DryRun{Out: env.out}, plan, &publish.Progress{}, nil)
	}

	if recorded {
		if rec.Site != *site || rec.MP3 != export.Name || rec.Size != export.Size {
			return fmt.Errorf("the upload record %s is for %s on %s (%d bytes), not for this MP3 and site. If that upload is finished with, delete the record to start again",
				path, rec.MP3, rec.Site, rec.Size)
		}
		if err := publish.Verify(ctx, client, rec.Progress); err != nil {
			if errors.Is(err, publish.ErrGone) {
				return fmt.Errorf("%w. It is not recreated automatically. If it was deleted on purpose, delete the record %s and run this again", err, path)
			}
			return err
		}
		fmt.Fprintf(env.out, "\nCarrying on with the upload begun %s.\n", rec.Updated.In(time.Local).Format("2 January at 15:04"))
	} else {
		rec = &record{Session: export.Session.ID, Site: *site, MP3: export.Name, Size: export.Size}
	}

	if !*yes && !confirm(env.out, answers, fmt.Sprintf("\nType yes to upload this talk as %s: ", plan.Status), "yes") {
		return errors.New("not confirmed; nothing was changed")
	}
	save := func(progress publish.Progress) error {
		rec.Progress = progress
		return rec.save(path)
	}
	if err := publish.Run(ctx, client, plan, &rec.Progress, save); err != nil {
		if rec.Progress != (publish.Progress{}) {
			return fmt.Errorf("%w\nWhat was created so far is recorded in %s; run the same command again to carry on from there", err, path)
		}
		return err
	}
	fmt.Fprintf(env.out, "\nUploaded as %s: %s\n", plan.Status, rec.Progress.Link)
	return nil
}

// removeUpload deletes what the recorded upload created, after showing exactly
// what that is and being told to go ahead. It always asks, -yes or not.
func removeUpload(ctx context.Context, env environment, answers *bufio.Scanner, client *publish.Client, rec *record, recorded bool, path string) error {
	if !recorded {
		return fmt.Errorf("there is no upload record for this session at %s, so there is nothing this command created to delete", path)
	}
	p := rec.Progress
	if p.PostID == 0 && p.MediaID == 0 {
		fmt.Fprintln(env.out, "The recorded upload created no talk or file.")
	} else {
		fmt.Fprintln(env.out, "\nThis will delete from the site:")
		if p.PostID != 0 {
			fmt.Fprintf(env.out, "  the talk, ID %d %s, which goes to the bin and can be restored from there\n", p.PostID, p.Link)
		}
		if p.MediaID != 0 {
			fmt.Fprintf(env.out, "  the MP3, ID %d, permanently: WordPress keeps no bin for files\n", p.MediaID)
		}
		if p.SpeakerID != 0 || p.SeriesID != 0 {
			fmt.Fprintln(env.out, "A speaker or series the upload added is kept.")
		}
		if !confirm(env.out, answers, "Type delete to go ahead: ", "delete") {
			return errors.New("not confirmed; nothing was deleted")
		}
		if err := publish.Remove(ctx, client, p); err != nil {
			return err
		}
	}
	done := strings.TrimSuffix(path, ".json") + ".deleted-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	if err := os.Rename(path, done); err != nil {
		return fmt.Errorf("deleted, but could not set the record aside: %w", err)
	}
	fmt.Fprintf(env.out, "Deleted. The record is kept as %s.\n", done)
	return nil
}

// confirm asks a question and reports whether the answer was the given word.
func confirm(out io.Writer, answers *bufio.Scanner, prompt, word string) bool {
	fmt.Fprint(out, prompt)
	if !answers.Scan() {
		fmt.Fprintln(out)
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answers.Text()), word)
}

// readTalk reads the talk file, refusing keys it does not know so that a
// misspelt key such as "newSpeeker" is caught rather than silently ignored.
// Keys are matched regardless of case, so "newspeaker" is read as "newSpeaker".
func readTalk(path string) (publish.Talk, error) {
	var talk publish.Talk
	data, err := os.ReadFile(path)
	if err != nil {
		return talk, fmt.Errorf("could not read the talk file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&talk); err != nil {
		return talk, fmt.Errorf("could not read the talk file %s: %w", path, err)
	}
	return talk, nil
}

// describe prints the plan as the operator would check it.
func describe(out io.Writer, plan *publish.Plan) {
	termName := func(term publish.Term) string {
		if term.ID == 0 {
			return term.Name + " (new)"
		}
		return term.Name
	}
	series := "none"
	if plan.Series != nil {
		series = termName(*plan.Series)
	}
	books := make([]string, len(plan.Books))
	for i, book := range plan.Books {
		books[i] = book.Name
	}
	podcast := "yes"
	if !plan.Podcast {
		podcast = "no"
	}
	fmt.Fprintf(out, `
Title:    %s
Date:     %s
Church:   %s
Speaker:  %s
Series:   %s
Passage:  %s
Books:    %s
MP3:      %s, %.1f MB, %s
Status:   %s
Podcast:  %s
`, plan.Title, plan.Started.In(time.Local).Format("Monday 2 January 2006, 15:04"), plan.Church.Name,
		termName(plan.Speaker), series, plan.Passage, strings.Join(books, ", "),
		plan.FileName, float64(plan.Size)/1e6, plan.Duration.Round(time.Second), plan.Status, podcast)
}
