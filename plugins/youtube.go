package plugins

import (
	"log"

	"github.com/jeffreymkabot/ytdl"
)

type Youtube struct{}

func (yt *Youtube) DownloadURL(arg string) (*Metadata, error) {
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
		DownloadURL: dlUrl.String(),
		Title:       info.Title,
		Duration:    info.Duration,
	}
	return md, nil
}
