package plugins

import (
	"io"
	"log"
	"net/url"
	"os/exec"
	"time"
)

type Plugin interface {
	CanHandle(string) bool
	Resolve(string) (Metadata, error)
}

type Metadata struct {
	Title    string
	Duration time.Duration
	Open     func() (io.ReadCloser, error)
}

// Streamlink is a generic plugin capable of handling a large variety of urls.
// It should be considered last in order to prioritize more narrowly focused plugins.
type Streamlink struct{}

func (sl Streamlink) CanHandle(arg string) bool {
	// fail fast to avoid launching another process
	url, err := url.Parse(arg)
	if err != nil || !url.IsAbs() {
		return false
	}
	streamlink := exec.Command(
		"streamlink",
		"--can-handle-url",
		arg,
	)
	return streamlink.Run() == nil
}

func (sl Streamlink) Resolve(arg string) (md Metadata, err error) {
	md = Metadata{
		Title:    arg,
		Duration: 0,
		// guess at the name of audio only streams that might be available
		Open: streamlinkOpener(arg, "audio,audio_only,480p,720p,best"),
	}
	return
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
