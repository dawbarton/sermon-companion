// Command sermon-publish uploads a finished Sermon Companion session to the
// church website as a talk: the MP3, its title, speaker, series, and Bible
// passage. It is the standalone stage of automatic upload described in
// docs/SERMON-UPLOAD-DESIGN.md. It reads a session folder and a small file of
// talk details, and never writes into the session, so that the website side can
// be got wrong repeatedly on a weekday without any risk to Sunday.
//
// At this stage it only plans: -dry-run signs in, looks every term up on the
// site, and prints the requests it would make. It reads from the site but
// changes nothing.
//
// Usage:
//
//	go run ./cmd/sermon-publish \
//	  -site https://www.emmanuelbristol.org.uk \
//	  -user uploads@example.org -password-file work/app-password.txt \
//	  -session "$HOME/Library/Application Support/Sermon Companion/sessions/<id>" \
//	  -talk work/talk.json -status private -podcast=false -dry-run
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
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dawbarton/sermon-companion/internal/publish"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sermon-publish:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("sermon-publish", flag.ContinueOnError)
	site := flags.String("site", "", "address of the WordPress site, starting https://")
	user := flags.String("user", "", "WordPress user name or email address the application password belongs to")
	passwordFile := flags.String("password-file", "", "file holding the application password")
	sessionDir := flags.String("session", "", "the session folder to upload from")
	talkFile := flags.String("talk", "", "JSON file of the talk's title, speaker, series, and passage")
	status := flags.String("status", "", "publish, private, or draft; there is deliberately no default")
	podcast := flags.Bool("podcast", true, "include the talk in the site's podcast feed")
	ffprobe := flags.String("ffprobe", "ffprobe", "FFprobe program, used to read the MP3's duration")
	timeout := flags.Duration("timeout", 30*time.Minute, "time allowed for the whole upload")
	dryRun := flags.Bool("dry-run", false, "print the requests instead of making them")
	if err := flags.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{"site": *site, "user": *user, "password-file": *passwordFile, "session": *sessionDir, "talk": *talkFile, "status": *status} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("-%s is required", name)
		}
	}
	if !*dryRun {
		return errors.New("only -dry-run is available so far; carrying out an upload is the next step of the design")
	}

	password, err := os.ReadFile(*passwordFile)
	if err != nil {
		return fmt.Errorf("could not read the password file: %w", err)
	}
	export, err := publish.LoadExport(*sessionDir)
	if err != nil {
		return err
	}
	talk, err := readTalk(*talkFile)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	duration, err := publish.ProbeDuration(ctx, *ffprobe, export.Path)
	if err != nil {
		return err
	}
	client, err := publish.NewClient(*site, *user, strings.TrimSpace(string(password)), &http.Client{})
	if err != nil {
		return err
	}
	account, err := client.Me(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Signed in as %s (%s).\n", account.Slug, strings.Join(account.Roles, ", "))
	if !account.Capabilities["upload_files"] {
		return errors.New("the account may not upload files")
	}
	if *status == "publish" && !account.Capabilities["publish_posts"] {
		return errors.New("the account may not publish; use -status draft or give it the Author role")
	}

	plan, warnings, err := publish.Prepare(ctx, client, export, talk, publish.Options{Status: *status, Podcast: *podcast}, duration)
	if err != nil {
		return err
	}
	describe(out, plan)
	for _, warning := range warnings {
		fmt.Fprintln(out, "Warning:", warning)
	}

	fmt.Fprintln(out, "\nThe requests below would be made in this order. Negative IDs stand for ones the site would give.")
	return publish.Run(ctx, &publish.DryRun{Out: out}, plan, &publish.Progress{}, nil)
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
