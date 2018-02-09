package plugins

import (
	"io"
	"time"
)

type Metadata struct {
	Title    string
	Duration time.Duration
	Open     func() (io.ReadCloser, error)
}

type Plugin interface {
	Resolve(string) (*Metadata, error)
}
