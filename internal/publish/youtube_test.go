package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelVideosReadsTheFeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel_id") != "UCReU3dVxdLKPuSs59dBgG6Q" {
			http.NotFound(w, r)
			return
		}
		// Trimmed from the real feed, namespaces and all.
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">
 <title>Emmanuel Bishopston</title>
 <published>2020-03-15T23:18:48+00:00</published>
 <entry>
  <id>yt:video:FqAJchBCktM</id>
  <yt:videoId>FqAJchBCktM</yt:videoId>
  <title>What's Wrong with Us? Sunday 1 November 2026</title>
  <published>2026-11-01T10:01:42+00:00</published>
  <media:group><media:title>What's Wrong with Us? Sunday 1 November 2026</media:title></media:group>
 </entry>
 <entry>
  <yt:videoId>LT-yW34lR-8</yt:videoId>
  <title>Life Together, Sunday 25 October 2026</title>
  <published>2026-10-26T00:45:10+00:00</published>
 </entry>
</feed>`))
	}))
	defer server.Close()
	previous := feedAddress
	feedAddress = server.URL
	defer func() { feedAddress = previous }()

	videos, err := ChannelVideos(context.Background(), server.Client(), "UCReU3dVxdLKPuSs59dBgG6Q")
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 2 || videos[0].ID != "FqAJchBCktM" || !strings.HasPrefix(videos[0].Title, "What's Wrong") || videos[1].Published.Day() != 26 {
		t.Fatalf("videos = %#v", videos)
	}
	if _, err := ChannelVideos(context.Background(), server.Client(), "UCwrong"); err == nil || !strings.Contains(err.Error(), "channel ID") {
		t.Fatalf("an unknown channel gave %v", err)
	}
}
