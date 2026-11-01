// Checking and undoing an earlier upload. A resumed upload first confirms that
// the talk and file it recorded are still on the site, because attaching audio
// to a talk someone has since deleted, or quietly creating a second talk, is
// worse than stopping to say so. Remove takes away what one upload created, for
// clearing up after a test.
package publish

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ErrGone marks something an earlier attempt created that is no longer on the
// site, or is in its bin.
var ErrGone = errors.New("is no longer on the site")

// Delete sends a DELETE request to a route.
func (c *Client) Delete(ctx context.Context, op, route string, query url.Values) error {
	_, err := c.do(ctx, op, http.MethodDelete, route, query, outgoing{}, nil)
	return err
}

// Verify checks that the talk and file in progress are still on the site and
// still belong together.
func Verify(ctx context.Context, c *Client, progress Progress) error {
	if progress.PostID != 0 {
		var post struct {
			Status string `json:"status"`
		}
		_, err := c.Get(ctx, "checking the talk an earlier attempt created", "/wp/v2/resource/"+strconv.Itoa(progress.PostID),
			url.Values{"context": {"edit"}, "_fields": {"id,status"}}, &post)
		if IsKind(err, KindNotFound) || (err == nil && post.Status == "trash") {
			return fmt.Errorf("the talk an earlier attempt created (ID %d) %w", progress.PostID, ErrGone)
		}
		if err != nil {
			return err
		}
	}
	if progress.MediaID != 0 {
		var media struct {
			Post int `json:"post"`
		}
		_, err := c.Get(ctx, "checking the MP3 an earlier attempt uploaded", "/wp/v2/media/"+strconv.Itoa(progress.MediaID),
			url.Values{"context": {"edit"}, "_fields": {"id,post"}}, &media)
		if IsKind(err, KindNotFound) {
			return fmt.Errorf("the MP3 an earlier attempt uploaded (ID %d) %w", progress.MediaID, ErrGone)
		}
		if err != nil {
			return err
		}
		if media.Post != progress.PostID {
			return fmt.Errorf("the MP3 an earlier attempt uploaded (ID %d) is now attached to post %d, not to the talk (ID %d)",
				progress.MediaID, media.Post, progress.PostID)
		}
	}
	return nil
}

// Remove deletes what an upload created. The MP3 is deleted permanently,
// because WordPress keeps no bin for attachments and refuses to delete one
// without force. The talk goes to the bin, from which it can be restored. A
// speaker or series the upload added is kept: a new speaker is real even when
// a test talk is not. Anything already gone counts as removed.
func Remove(ctx context.Context, c *Client, progress Progress) error {
	if progress.MediaID != 0 {
		err := c.Delete(ctx, "deleting the MP3", "/wp/v2/media/"+strconv.Itoa(progress.MediaID), url.Values{"force": {"true"}})
		if err != nil && !IsKind(err, KindNotFound) {
			return err
		}
	}
	if progress.PostID != 0 {
		err := c.Delete(ctx, "moving the talk to the bin", "/wp/v2/resource/"+strconv.Itoa(progress.PostID), nil)
		if err != nil && !IsKind(err, KindNotFound) {
			return err
		}
	}
	return nil
}
