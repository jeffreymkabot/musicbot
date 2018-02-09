package plugins

import (
	"io"
	"log"
	"net/http"

	"github.com/jeffreymkabot/ytdl"
)

type Youtube struct{}

func (yt *Youtube) Resolve(arg string) (*Metadata, error) {
	info, err := ytdl.GetVideoInfo(arg)
	if err != nil {
		return nil, err
	}

	dlUrl, err := info.GetDownloadURL(info.Formats.Extremes(ytdl.FormatAudioEncodingKey, true)[0])
	if err != nil {
		return nil, err
	}
	log.Printf("dl url %s", dlUrl)

	md := &Metadata{
		Title:    info.Title,
		Duration: info.Duration,
		Open: func() (io.ReadCloser, error) {
			resp, err := http.Get(dlUrl.String())
			return resp.Body, err
		},
	}
	return md, nil
}
