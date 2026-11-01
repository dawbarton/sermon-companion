// Carrying out a plan: the requests that change the site, in the order that
// leaves the least behind if one fails. The talk is created as a draft first,
// so nothing public exists until the end; the MP3 is uploaded attached to it;
// and a single last request points the Audio field at the file and sets the
// final status, so a published talk never lacks its audio. Progress records
// each ID as soon as the site gives it, so a failed upload can be resumed
// without creating anything twice.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// The site's ACF field names, from the resource schema read on 1 November 2026.
const (
	fieldDate        = "resource_date"
	fieldType        = "resource_type"
	fieldPassage     = "resource_bible_passages"
	fieldPodcast     = "resource_include_in_podcast_feed"
	fieldAudio       = "resource_audio"
	fieldAudioInfo   = "resource_audio_info"
	fieldAudioSize   = "resource_audio_file_size"
	fieldAudioTime   = "resource_audio_duration"
	fieldVideoSource = "resource_video_source"
	fieldVideoURL    = "resource_youtube_url"
)

// Sender makes the requests that change the site: the real Client, or a
// DryRun that prints them instead.
type Sender interface {
	CreateTerm(ctx context.Context, restBase, name string) (Term, error)
	SendJSON(ctx context.Context, op, method, route string, query url.Values, payload, out any) error
	UploadFile(ctx context.Context, op, route string, query url.Values, filename, contentType string, content io.Reader, size int64, out any) error
}

// Run carries out plan, skipping whatever progress says is already done, and
// calls save after every step that creates something. save may be nil.
func Run(ctx context.Context, s Sender, plan *Plan, progress *Progress, save func(Progress) error) error {
	record := func() error {
		if save == nil {
			return nil
		}
		return save(*progress)
	}
	// create adds a term the site does not have yet, once: the ID is recorded
	// by name, so that a retry finds it rather than adding it again.
	create := func(taxonomy string, term Term, created *map[string]int) (int, error) {
		if term.ID != 0 {
			return term.ID, nil
		}
		if id, ok := (*created)[term.Name]; ok {
			return id, nil
		}
		made, err := s.CreateTerm(ctx, taxonomy, term.Name)
		if err != nil {
			return 0, err
		}
		if *created == nil {
			*created = map[string]int{}
		}
		(*created)[term.Name] = made.ID
		return made.ID, record()
	}

	speakerIDs := make([]int, 0, len(plan.Speakers))
	for _, speaker := range plan.Speakers {
		id, err := create(speakerTaxonomy, speaker, &progress.Speakers)
		if err != nil {
			return err
		}
		speakerIDs = append(speakerIDs, id)
	}
	seriesIDs := []int{}
	if plan.Series != nil {
		id, err := create(seriesTaxonomy, *plan.Series, &progress.Series)
		if err != nil {
			return err
		}
		seriesIDs = append(seriesIDs, id)
	}

	if progress.PostID == 0 {
		var created struct {
			ID int `json:"id"`
		}
		if err := s.SendJSON(ctx, "creating the talk", http.MethodPost, "/wp/v2/resource", nil,
			talkPayload(plan, speakerIDs, seriesIDs, "draft", 0), &created); err != nil {
			return err
		}
		if created.ID == 0 {
			return fmt.Errorf("creating the talk failed: the site did not say what ID it gave it")
		}
		progress.PostID = created.ID
		if err := record(); err != nil {
			return err
		}
	}

	if progress.MediaID == 0 {
		file, err := os.Open(plan.File)
		if err != nil {
			return fmt.Errorf("opening the MP3: %w", err)
		}
		var media struct {
			ID int `json:"id"`
		}
		// The file is given the talk's title; otherwise WordPress takes the
		// title from the MP3's own tag, which is the service's.
		query := url.Values{"post": {strconv.Itoa(progress.PostID)}, "title": {plan.Title}}
		err = s.UploadFile(ctx, "uploading the MP3", "/wp/v2/media", query, plan.FileName, "audio/mpeg", file, plan.Size, &media)
		file.Close()
		if err != nil {
			return err
		}
		if media.ID == 0 {
			return fmt.Errorf("uploading the MP3 failed: the site did not say what ID it gave the file")
		}
		progress.MediaID = media.ID
		if err := record(); err != nil {
			return err
		}
	}

	if !progress.Finished {
		var finished struct {
			Link string `json:"link"`
		}
		if err := s.SendJSON(ctx, "attaching the audio and setting the status", http.MethodPost,
			"/wp/v2/resource/"+strconv.Itoa(progress.PostID), nil, talkPayload(plan, speakerIDs, seriesIDs, plan.Status, progress.MediaID), &finished); err != nil {
			return err
		}
		progress.Finished = true
		progress.Link = finished.Link
		if err := record(); err != nil {
			return err
		}
	}
	return nil
}

// talkPayload is everything the site is told about the talk. The talk is
// created as a draft with all of it but the audio, and the last request sends
// all of it again with the audio and the final status, so that details
// corrected between a failed attempt and its retry are what is published.
// ACF checks every acf object against the whole field group, whose type is
// required, so the type goes in every request.
func talkPayload(plan *Plan, speakerIDs, seriesIDs []int, status string, mediaID int) map[string]any {
	bookIDs := make([]int, len(plan.Books))
	for i, book := range plan.Books {
		bookIDs[i] = book.ID
	}
	podcast := "No"
	if plan.Podcast {
		podcast = "Yes"
	}
	acf := map[string]any{
		fieldDate:    plan.LocalDate,
		fieldType:    plan.Type.ID,
		fieldPassage: plan.Passage,
		fieldPodcast: podcast,
	}
	if plan.VideoURL != "" {
		acf[fieldVideoSource] = "YouTube"
		acf[fieldVideoURL] = plan.VideoURL
	}
	if mediaID != 0 {
		acf[fieldAudio] = mediaID
		acf[fieldAudioInfo] = map[string]any{
			fieldAudioSize: plan.Size,
			fieldAudioTime: formatDuration(plan.Duration),
		}
	}
	return map[string]any{
		"title":          plan.Title,
		"status":         status,
		"date_gmt":       plan.PostDate.UTC().Format("2006-01-02T15:04:05"),
		"featured_media": plan.Image.ID,
		churchTaxonomy:   []int{plan.Church.ID},
		typeTaxonomy:     []int{plan.Type.ID},
		speakerTaxonomy:  speakerIDs,
		seriesTaxonomy:   seriesIDs,
		bookTaxonomy:     bookIDs,
		"acf":            acf,
	}
}

// DryRun prints each request instead of sending it, and answers as the site
// would, with negative placeholder IDs, so that the whole sequence can be read
// through before anything is changed.
type DryRun struct {
	Out    io.Writer
	step   int
	lastID int
}

func (d *DryRun) placeholder() int {
	d.lastID--
	return d.lastID
}

func (d *DryRun) heading(op, method, route string, query url.Values) {
	d.step++
	address := "/wp-json" + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	fmt.Fprintf(d.Out, "\n%d. %s\n   %s %s\n", d.step, op, method, address)
}

func (d *DryRun) body(payload any) {
	encoded, _ := json.MarshalIndent(payload, "   ", "  ")
	fmt.Fprintf(d.Out, "   %s\n", encoded)
}

// CreateTerm prints the request that would add a term.
func (d *DryRun) CreateTerm(ctx context.Context, restBase, name string) (Term, error) {
	d.heading(fmt.Sprintf("adding %q to the %s list", name, restBase), http.MethodPost, "/wp/v2/"+restBase, nil)
	d.body(map[string]string{"name": name})
	return Term{ID: d.placeholder(), Name: name}, nil
}

// SendJSON prints a JSON request and answers with a placeholder ID.
func (d *DryRun) SendJSON(ctx context.Context, op, method, route string, query url.Values, payload, out any) error {
	d.heading(op, method, route, query)
	d.body(payload)
	return d.answer(out, `"link":"(the address the site will give the talk)"`)
}

// UploadFile prints the upload without reading the file.
func (d *DryRun) UploadFile(ctx context.Context, op, route string, query url.Values, filename, contentType string, content io.Reader, size int64, out any) error {
	disposition, err := attachmentDisposition(filename)
	if err != nil {
		return err
	}
	d.heading(op, http.MethodPost, route, query)
	fmt.Fprintf(d.Out, "   Content-Type: %s\n   Content-Disposition: %s\n   (%d bytes of audio)\n", contentType, disposition, size)
	return d.answer(out, "")
}

func (d *DryRun) answer(out any, extra string) error {
	if out == nil {
		return nil
	}
	fields := []string{fmt.Sprintf(`"id":%d`, d.placeholder())}
	if extra != "" {
		fields = append(fields, extra)
	}
	return json.Unmarshal([]byte("{"+strings.Join(fields, ",")+"}"), out)
}
