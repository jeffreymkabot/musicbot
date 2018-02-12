package plugins

import (
	"io"
	"log"
	"os/exec"
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

// format is comma separated list of stream precedence e.g. "480p,best"
// "best"/"worst" meta formats are always available
func streamlinkOpener(url string, format string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		streamlink := exec.Command(
			"streamlink",
			"-O",
			url,
			format,
		)
		stdout, err := streamlink.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := streamlink.Start(); err != nil {
			return nil, err
		}
		log.Printf("started streamlink targetting %v", url)
		return streamlinkReadCloser{stdout, streamlink}, nil
	}
}

type streamlinkReadCloser struct {
	io.Reader
	streamlink *exec.Cmd
}

func (slrc streamlinkReadCloser) Close() error {
	err := slrc.streamlink.Process.Kill()
	log.Printf("killed streamlink %v", err)
	err = slrc.streamlink.Wait()
	log.Printf("closed streamlink %v", err)
	return err
}
