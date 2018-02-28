package plugins

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"

	"github.com/jeffreymkabot/ytdl"
)

var urlRegexpYt = regexp.MustCompile(`youtube\.com|youtu\.be`)

type Youtube struct{}

func (yt Youtube) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpYt.MatchString(url.Hostname())
}

func (yt Youtube) Resolve(arg string) (md Metadata, err error) {
	info, err := ytdl.GetVideoInfo(arg)
	if err != nil {
		return
	}

	if info.Livestream {
		// found that audio_mp4 format always cut out after 2seconds
		md = Metadata{
			Title:    info.Title,
			Duration: info.Duration,
			Open:     streamlinkOpener(arg, "480p,720p,best"),
		}
		return
	}

	dlUrl, err := info.GetDownloadURL(info.Formats.Extremes(ytdl.FormatAudioEncodingKey, true)[0])
	if err != nil {
		return
	}
	log.Printf("dl url %s", dlUrl)

	md = Metadata{
		Title:    info.Title,
		Duration: info.Duration,
		Open: func() (io.ReadCloser, error) {
			resp, err := http.Get(dlUrl.String())
			return resp.Body, err
		},
	}
	return
}
