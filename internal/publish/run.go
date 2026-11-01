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
	fieldDate      = "resource_date"
	fieldType      = "resource_type"
	fieldPassage   = "resource_bible_passages"
	fieldPodcast   = "resource_include_in_podcast_feed"
	fieldAudio     = "resource_audio"
	fieldAudioInfo = "resource_audio_info"
	fieldAudioSize = "resource_audio_file_size"
	fieldAudioTime = "resource_audio_duration"
)

// Sender makes the requests that change the site: the real Client, or a
// DryRun that prints them instead.
type Sender interface {
	CreateTerm(ctx context.Context, restBase, name string) (Term, error)
	SendJSON(ctx context.Context, op, method, route string, query url.Values, payload, out any) error
	UploadFile(ctx context.Context, op, route string, query url.Values, filename, contentType string, content io.Reader, size int64, out any) error
}

// Progress records what an upload has created on the site so far.
type Progress struct {
	SpeakerID int    `json:"speakerId,omitempty"`
	SeriesID  int    `json:"seriesId,omitempty"`
	PostID    int    `json:"postId,omitempty"`
	MediaID   int    `json:"mediaId,omitempty"`
	Finished  bool   `json:"finished,omitempty"`
	Link      string `json:"link,omitempty"`
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

	speakerID := plan.Speaker.ID
	if speakerID == 0 {
		if progress.SpeakerID == 0 {
			term, err := s.CreateTerm(ctx, speakerTaxonomy, plan.Speaker.Name)
			if err != nil {
				return err
			}
			progress.SpeakerID = term.ID
			if err := record(); err != nil {
				return err
			}
		}
		speakerID = progress.SpeakerID
	}

	var seriesIDs []int
	if plan.Series != nil {
		seriesID := plan.Series.ID
		if seriesID == 0 {
			if progress.SeriesID == 0 {
				term, err := s.CreateTerm(ctx, seriesTaxonomy, plan.Series.Name)
				if err != nil {
					return err
				}
				progress.SeriesID = term.ID
				if err := record(); err != nil {
					return err
				}
			}
			seriesID = progress.SeriesID
		}
		seriesIDs = []int{seriesID}
	}

	if progress.PostID == 0 {
		var created struct {
			ID int `json:"id"`
		}
		if err := s.SendJSON(ctx, "creating the talk", http.MethodPost, "/wp/v2/resource", nil,
			draftPayload(plan, speakerID, seriesIDs), &created); err != nil {
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
		err = s.UploadFile(ctx, "uploading the MP3", "/wp/v2/media", url.Values{"post": {strconv.Itoa(progress.PostID)}},
			plan.FileName, "audio/mpeg", file, plan.Size, &media)
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
			"/wp/v2/resource/"+strconv.Itoa(progress.PostID), nil, finishPayload(plan, progress.MediaID), &finished); err != nil {
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

// draftPayload creates the talk with everything except its audio, as a draft.
func draftPayload(plan *Plan, speakerID int, seriesIDs []int) map[string]any {
	bookIDs := make([]int, len(plan.Books))
	for i, book := range plan.Books {
		bookIDs[i] = book.ID
	}
	if seriesIDs == nil {
		seriesIDs = []int{}
	}
	podcast := "No"
	if plan.Podcast {
		podcast = "Yes"
	}
	return map[string]any{
		"title":         plan.Title,
		"status":        "draft",
		"date_gmt":      plan.Started.UTC().Format("2006-01-02T15:04:05"),
		churchTaxonomy:  []int{plan.Church.ID},
		typeTaxonomy:    []int{plan.Type.ID},
		speakerTaxonomy: []int{speakerID},
		seriesTaxonomy:  seriesIDs,
		bookTaxonomy:    bookIDs,
		"acf": map[string]any{
			fieldDate:    plan.LocalDate,
			fieldType:    plan.Type.ID, // required by the field group, as one term ID
			fieldPassage: plan.Passage,
			fieldPodcast: podcast,
		},
	}
}

// finishPayload points the Audio field at the uploaded file and sets the final
// status in the same request.
func finishPayload(plan *Plan, mediaID int) map[string]any {
	return map[string]any{
		"status": plan.Status,
		"acf": map[string]any{
			fieldAudio: mediaID,
			fieldAudioInfo: map[string]any{
				fieldAudioSize: plan.Size,
				fieldAudioTime: formatDuration(plan.Duration),
			},
		},
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
