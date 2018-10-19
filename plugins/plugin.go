package plugins

import (
	"bytes"
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

type VideoPlugin interface {
	Plugin
}

type Metadata struct {
	Title    string
	Duration time.Duration
	OpenFunc func() (io.ReadCloser, error)
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
		OpenFunc: streamlinkOpener(arg, "audio,audio_only,480p,720p,best"),
	}
	return
}

// format is comma separated list of stream precedence e.g. "480p,best"
// "best"/"worst" meta formats are always available
func streamlinkOpener(url string, format string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		streamlink := exec.Command(
			"streamlink",
			"-O", // tells streamlink to write to stdout
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

// produce a second stream of images from the source, if the source supports it
func splitVideoStream(openSrc func() (io.ReadCloser, error)) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		src, err := openSrc()
		if err != nil {
			return nil, err
		}

		// a read from tee causes the same read to be available on buf
		// when the player reads from tee it will fork the source and make the same bytes available to ffmpeg
		buf := &bytes.Buffer{}
		tee := io.TeeReader(src, buf)

		ffmpeg := exec.Command(
			"ffmpeg",
			"-i", "pipe:0", // tells ffmpeg to read from stdin
			"-f", "image2",
			"-vf", "fps=1/5", // one image every 5 seconds
			"-update", "1", // tells ffmpeg to continuously overwrite the image
			"pipe:1", // tells ffmpeg to write to stdout
		)
		ffmpeg.Stdin = buf
		stdout, err := ffmpeg.StdoutPipe()
		// TODO stdout is a stream of video frames
		if err != nil {
			return nil, err
		}
		_ = stdout

		return splitVideoReadCloser{Reader: tee, orig: src, ffmpeg: ffmpeg}, nil
	}
}

type splitVideoReadCloser struct {
	io.Reader
	orig   io.Closer
	ffmpeg *exec.Cmd
}

func (svrc splitVideoReadCloser) Close() error {
	svrc.orig.Close()
	err := svrc.ffmpeg.Process.Kill()
	log.Printf("killed forked video ffmpeg %v", err)
	err = svrc.ffmpeg.Wait()
	log.Printf("closed forked video ffmpeg %v", err)
	return err
}
