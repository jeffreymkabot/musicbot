package plugins

import "time"

type Metadata struct {
	DownloadURL string
	Title       string
	Duration    time.Duration
}

type Plugin interface {
	DownloadURL(string) (*Metadata, error)
}
