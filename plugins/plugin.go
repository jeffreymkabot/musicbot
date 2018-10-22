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
	ResolveWithVideo(string) (Metadata, error)
}

type Metadata struct {
	Title                 string
	Duration              time.Duration
	OpenAudioStream       func() (io.ReadCloser, error)
	OpenAudioVideoStreams func() (audio io.ReadCloser, video io.ReadCloser, err error)
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
		OpenAudioStream: streamlinkOpener(arg, "audio,audio_only,480p,720p,best"),
	}
	return
}

func (sl Streamlink) ResolveWithVideo(arg string) (md Metadata, err error) {
	openStream := streamlinkOpener(arg, "480p,720p,best")
	md = Metadata{
		Title:                 arg,
		Duration:              0,
		OpenAudioStream:       openStream,
		OpenAudioVideoStreams: splitVideoStream(openStream),
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

// produce a separate stream of images sampled from the base stream
// the image stream is synchronized with the base stream
// the image stream can be closed without affecting the base stream
// but closing the base stream will stop the image stream
func splitVideoStream(openSrc func() (io.ReadCloser, error)) func() (audio io.ReadCloser, videoSamples io.ReadCloser, err error) {
	return func() (audio io.ReadCloser, videoSamples io.ReadCloser, err error) {
		src, err := openSrc()
		if err != nil {
			return
		}

		// reads from src stream copy their bytes into buf
		// a separate ffmpeg reads from buf and outputs sampled images to ffmpeg's stdout

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
		// stdout gets closed when ffmpeg.Wait() is called
		stdout, err := ffmpeg.StdoutPipe()
		if err != nil {
			src.Close()
			return
		}
		if err = ffmpeg.Start(); err != nil {
			src.Close()
			return
		}

		audio = struct {
			io.Reader
			io.Closer
		}{
			Reader: tee,
			Closer: src,
		}
		videoSamples = videoSampleReadCloser{
			Reader: stdout,
			ffmpeg: ffmpeg,
		}
		log.Printf("started ffmpeg to sample images from stream")
		return
	}
}

type videoSampleReadCloser struct {
	io.Reader
	ffmpeg *exec.Cmd
}

func (vsrc videoSampleReadCloser) Close() error {
	err := vsrc.ffmpeg.Process.Kill()
	log.Printf("killed ffmpeg video sampler %v", err)
	// ffmpeg.Wait() also closes ffmpeg's StdoutPipe
	err = vsrc.ffmpeg.Wait()
	log.Printf("closed ffmpeg video sampler %v", err)
	return err
}
