package plugins

import (
	"io"
	"log"
	"net/http"

	"github.com/jeffreymkabot/ytdl"
)

type Youtube struct{}

func (yt Youtube) Resolve(arg string) (*Metadata, error) {
	info, err := ytdl.GetVideoInfo(arg)
	if err != nil {
		return nil, err
	}

	md := &Metadata{
		Title:    info.Title,
		Duration: info.Duration,
	}

	if info.Livestream {
		// found that audio_mp4 format always cut out after 2seconds
		md.Open = streamlinkOpener(arg, "480p,720p,best")
		return md, nil
	}

	dlUrl, err := info.GetDownloadURL(info.Formats.Extremes(ytdl.FormatAudioEncodingKey, true)[0])
	if err != nil {
		return nil, err
	}
	log.Printf("dl url %s", dlUrl)

	md.Open = func() (io.ReadCloser, error) {
		resp, err := http.Get(dlUrl.String())
		return resp.Body, err
	}
	return md, nil
}
