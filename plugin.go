package music

import (
	"io"
)

type Plugin interface {
	Search(string) ([]string, error)
	Stream(string) (io.Reader, error)
}