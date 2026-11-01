// Finding the service's video on the church's YouTube channel. The channel's
// public feed lists its latest fifteen videos, live streams included, and
// needs no API key. A stream's publication time is not reliably on the day
// of the service (one for a Sunday was published at 00:45 on the Monday), so
// the video chosen is the first published in a window around the recording,
// and the operator sees its title and can change it.
package publish

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Video is one entry in a channel's feed.
type Video struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Published time.Time `json:"published"`
}

// URL is the address the site's YouTube URL field is given.
func (v Video) URL() string { return "https://www.youtube.com/watch?v=" + url.QueryEscape(v.ID) }

// feedAddress is where YouTube publishes a channel's feed. It is a variable
// so that the tests can point it at a local server.
var feedAddress = "https://www.youtube.com/feeds/videos.xml"

// ChannelVideos reads the channel's feed.
func ChannelVideos(ctx context.Context, httpClient *http.Client, channelID string) ([]Video, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedAddress+"?channel_id="+url.QueryEscape(channelID), nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("YouTube could not be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube did not return the channel's videos (HTTP %d); check the channel ID in the settings", response.StatusCode)
	}
	var feed struct {
		Entries []struct {
			VideoID   string `xml:"videoId"`
			Title     string `xml:"title"`
			Published string `xml:"published"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&feed); err != nil {
		return nil, fmt.Errorf("YouTube's list of the channel's videos could not be read: %w", err)
	}
	videos := make([]Video, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		published, err := time.Parse(time.RFC3339, entry.Published)
		if err != nil || entry.VideoID == "" {
			continue
		}
		videos = append(videos, Video{ID: entry.VideoID, Title: entry.Title, Published: published})
	}
	return videos, nil
}

// The window a service's video is looked for in: a stream starts before the
// recording does, and has been published as much as a day and a half after.
const (
	videoLead = 3 * time.Hour
	videoLag  = 36 * time.Hour
)

// ChooseVideo picks the service's video: the first published within the
// window around the start of the recording.
func ChooseVideo(videos []Video, started time.Time) (Video, bool) {
	candidates := make([]Video, 0, len(videos))
	for _, video := range videos {
		if !video.Published.Before(started.Add(-videoLead)) && !video.Published.After(started.Add(videoLag)) {
			candidates = append(candidates, video)
		}
	}
	if len(candidates) == 0 {
		return Video{}, false
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Published.Before(candidates[j].Published) })
	return candidates[0], true
}
