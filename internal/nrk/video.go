package nrk

import (
	"log"
	"net/http"
)

// ClipVideo is NRK's video for a clip reference.
type ClipVideo struct {
	// URL is NRK's adaptive HLS master playlist, used as is.
	URL string
	// DurationSeconds is the clip's length, to match it with the audio.
	DurationSeconds float64
}

// VideoForClip looks up a clip's playback manifest. Found means a
// playable, unencrypted HLS video; NotFound means NRK has no usable video
// for it (removed, not video, not playable); Unavailable is transient.
func (c *Client) VideoForClip(clipID string) (*ClipVideo, Outcome) {
	var m clipManifest
	status, ok := c.up.getJSON(API+"/playback/manifest/clip/"+clipID, &m)
	switch {
	case isTransient(status):
		return nil, Unavailable
	case status == http.StatusNotFound:
		return nil, NotFound
	case status != http.StatusOK || !ok:
		log.Printf("Unexpected clip manifest status %d for %s", status, clipID)
		return nil, Unavailable
	}
	if m.SourceMedium != "video" || m.Playability != "playable" || m.Playable == nil {
		return nil, NotFound
	}
	for _, asset := range m.Playable.Assets {
		if asset != nil && asset.URL != "" && !asset.Encrypted && IsHLS(asset.URL) {
			seconds, err := DurationSeconds(m.Playable.Duration)
			if err != nil {
				return nil, NotFound
			}
			return &ClipVideo{URL: asset.URL, DurationSeconds: seconds}, Found
		}
	}
	return nil, NotFound
}
