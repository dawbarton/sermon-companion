// Uploading a service's MP3 to the church website from the upload page. The
// upload runs in the background like making the MP3 does, and every ID the
// site gives is recorded in the session's journal as soon as it is given, so
// that a failed upload carries on from where it stopped when Publish is
// pressed again rather than creating anything twice.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/publish"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// publishTimeout bounds a whole upload. A service's MP3 is 20 to 40 MB, which
// takes a few minutes on a slow church connection.
const publishTimeout = 30 * time.Minute

// publishJob is the key an upload's cancel function is kept under in jobs,
// beside the session's own key for making the MP3.
func publishJob(id string) string { return id + "#publish" }

// busy reports whether the session's MP3 is being made or uploaded.
func (s *Server) busy(id string) (exporting, publishing bool) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	_, exporting = s.jobs[id]
	_, publishing = s.jobs[publishJob(id)]
	return exporting, publishing
}

func (s *Server) startPublish(w http.ResponseWriter, r *http.Request) {
	var request struct {
		// AllowDuplicate is the operator's answer to being told the website
		// already has a talk for this church and day.
		AllowDuplicate bool `json:"allowDuplicate"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
		return
	}
	c := s.settings.Get()
	if c.Publish == nil {
		writeError(w, http.StatusConflict, errors.New("uploading to the website is not set up in the settings file"))
		return
	}
	password, err := s.settings.Password()
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	id := r.PathValue("id")
	dir, err := s.store.SessionDir(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}

	ctx, cancel := context.WithTimeout(s.background, publishTimeout)
	s.jobsMu.Lock()
	if s.closing {
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusServiceUnavailable, errors.New("Sermon Companion is closing"))
		return
	}
	if _, running := s.jobs[publishJob(id)]; running {
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, errors.New("this talk is already being uploaded"))
		return
	}
	// Checked under the same lock that registers the upload, as the export
	// checks for an upload, so that the two cannot both start: the export
	// would replace the MP3 the upload is reading.
	if _, exporting := s.jobs[id]; exporting {
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, errors.New("wait for the MP3 to be created before publishing it"))
		return
	}
	s.jobs[publishJob(id)] = cancel
	s.jobsWG.Add(1)
	s.jobsMu.Unlock()
	finish := func() {
		s.jobsMu.Lock()
		if cancel, ok := s.jobs[publishJob(id)]; ok {
			cancel()
			delete(s.jobs, publishJob(id))
		}
		s.jobsMu.Unlock()
		s.jobsWG.Done()
	}

	site := strings.TrimSuffix(strings.TrimSpace(c.Publish.Site), "/")
	var export *publish.Export
	session, err := s.store.UpdateAtRevision(id, &expected, "publish.started", nil, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("stop the recording before publishing")
		}
		if session.Talk == nil {
			return errors.New("fill in the talk's details before publishing")
		}
		if session.Publish != nil && session.Publish.Progress.Finished {
			return errors.New("this talk is already on the website")
		}
		found, err := publish.ExportOf(*session, dir)
		if err != nil {
			return err
		}
		export = found
		previous := session.Publish
		info := &store.PublishInfo{Status: "running", StartedAt: time.Now().UTC(), Site: site, MP3: session.Export.Output, MP3Finished: session.Export.EndedAt}
		if previous != nil && previous.Site == site {
			// Carry on from an earlier attempt on the same site. Which MP3
			// was uploaded stays as recorded until the upload replaces it.
			info.Progress = previous.Progress
			if previous.Progress.MediaID != 0 {
				info.MP3, info.MP3Finished = previous.MP3, previous.MP3Finished
			}
		}
		session.Publish = info
		return nil
	})
	if err != nil {
		finish()
		writeSessionMutationError(w, err)
		return
	}
	go func() {
		defer finish()
		s.runPublish(ctx, session, export, site, password, request.AllowDuplicate)
	}()
	s.writeSession(w, http.StatusAccepted, session)
}

// runPublish carries out the upload and records how it ended.
func (s *Server) runPublish(ctx context.Context, session *store.Session, export *publish.Export, site, password string, allowDuplicate bool) {
	id := session.ID
	log.Printf("upload of %s to %s started", id, site)
	link, err := s.uploadTalk(ctx, session, export, site, password, allowDuplicate)
	ended := time.Now().UTC()
	if err != nil {
		message := explainPublishError(ctx, err)
		log.Printf("upload of %s failed: %s", id, message)
		if _, updateErr := s.store.Update(id, "publish.failed", map[string]any{"error": message}, func(session *store.Session) error {
			session.Publish.Status, session.Publish.EndedAt, session.Publish.Error = "failed", &ended, message
			return nil
		}); updateErr != nil {
			log.Printf("upload of %s: recording the failure: %v", id, updateErr)
		}
		return
	}
	log.Printf("upload of %s finished: %s", id, link)
	if _, err := s.store.Update(id, "publish.completed", map[string]any{"link": link}, func(session *store.Session) error {
		session.Publish.Status, session.Publish.EndedAt, session.Publish.Error = "completed", &ended, ""
		return nil
	}); err != nil {
		log.Printf("upload of %s: recording that it finished: %v", id, err)
	}
}

func (s *Server) uploadTalk(ctx context.Context, session *store.Session, export *publish.Export, site, password string, allowDuplicate bool) (string, error) {
	c := s.settings.Get()
	client, err := publish.NewClient(site, c.Publish.User, password, s.publishHTTP)
	if err != nil {
		return "", err
	}
	account, err := client.Me(ctx)
	if err != nil {
		return "", err
	}
	if !account.Capabilities["upload_files"] {
		return "", errors.New("the website account may not upload files")
	}
	status := c.Publish.PublishStatus()
	if status == "publish" && !account.Capabilities["publish_posts"] {
		return "", errors.New("the website account may not publish; give it the Author role")
	}

	progress := session.Publish.Progress
	save := func(p publish.Progress) error {
		_, err := s.store.Update(session.ID, "publish.progress", p, func(stored *store.Session) error {
			if p.MediaID != 0 && p.MediaID != stored.Publish.Progress.MediaID {
				// A file has just been uploaded, and it is the current MP3.
				stored.Publish.MP3, stored.Publish.MP3Finished = session.Export.Output, session.Export.EndedAt
			}
			stored.Publish.Progress = p
			return nil
		})
		return err
	}
	if !progress.Empty() {
		if err := s.reconcileProgress(ctx, client, session, &progress); err != nil {
			return "", err
		}
		if err := save(progress); err != nil {
			return "", err
		}
	}

	duration, err := s.probeDuration(ctx, c.FFprobe, export.Path)
	if err != nil {
		return "", err
	}
	plan, warnings, err := publish.Prepare(ctx, client, export, *session.Talk, publish.Options{Status: status, Type: c.Publish.Type()}, duration)
	if err != nil {
		return "", err
	}
	var others []publish.ExistingTalk
	for _, duplicate := range plan.Duplicates {
		if duplicate.ID != progress.PostID {
			others = append(others, duplicate)
		}
	}
	if len(others) > 0 && !allowDuplicate {
		return "", fmt.Errorf("the website already has a talk for this church and day: %q, %s. Nothing was uploaded; publish again and confirm to add this one as well", others[0].Title, others[0].Link)
	}
	for _, warning := range warnings {
		log.Printf("upload of %s: %s", session.ID, warning)
	}
	if err := publish.Run(ctx, client, plan, &progress, save); err != nil {
		return "", err
	}
	return progress.Link, nil
}

// reconcileProgress checks what an earlier attempt created before carrying
// on from it. A talk or file deleted on the site since is made again, and a
// file uploaded before the MP3 was made again is replaced by the new one.
func (s *Server) reconcileProgress(ctx context.Context, client *publish.Client, session *store.Session, progress *publish.Progress) error {
	deleteMedia := func(op string) error {
		err := client.Delete(ctx, op, "/wp/v2/media/"+strconv.Itoa(progress.MediaID), map[string][]string{"force": {"true"}})
		if err != nil && !publish.IsKind(err, publish.KindNotFound) {
			return err
		}
		progress.MediaID = 0
		return nil
	}
	talkOnly := *progress
	talkOnly.MediaID = 0
	if err := publish.Verify(ctx, client, talkOnly); errors.Is(err, publish.ErrGone) {
		log.Printf("upload of %s: %v; the talk will be created again", session.ID, err)
		if progress.MediaID != 0 {
			if err := deleteMedia("deleting the MP3 uploaded for the deleted talk"); err != nil {
				return err
			}
		}
		progress.PostID = 0
		return nil
	} else if err != nil {
		return err
	}
	if err := publish.Verify(ctx, client, *progress); errors.Is(err, publish.ErrGone) {
		log.Printf("upload of %s: %v; it will be uploaded again", session.ID, err)
		progress.MediaID = 0
		return nil
	} else if err != nil {
		return err
	}
	if progress.MediaID != 0 && session.Export != nil && !sameTime(session.Publish.MP3Finished, session.Export.EndedAt) {
		// The earlier file was never published, since the talk stays a draft
		// until the last step. It is deleted so that it does not linger in
		// the media library beside the one that replaces it.
		log.Printf("upload of %s: the MP3 was made again since it was uploaded, so the new one replaces it", session.ID)
		return deleteMedia("deleting the earlier MP3")
	}
	return nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// explainPublishError turns a failure into what the operator should be told,
// with what to do about it.
func explainPublishError(ctx context.Context, err error) string {
	message := publish.Capitalise(err.Error())
	if !strings.HasSuffix(message, ".") {
		message += "."
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return "Sermon Companion was closed during the upload. Publish again to carry on from where it stopped."
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "The upload took too long and was stopped. Check the internet connection, then publish again to carry on from where it stopped."
	case publish.IsKind(err, publish.KindNetwork), publish.IsKind(err, publish.KindTimeout):
		return message + " Check the internet connection, then publish again to carry on from where it stopped."
	case publish.IsKind(err, publish.KindBadCredentials):
		return message + " Check the password in " + config.PasswordFileName + ", beside the settings file."
	}
	return message
}
