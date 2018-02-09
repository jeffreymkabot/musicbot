package plugins

import (
	"io"
	"log"
	"os/exec"
)

type Twitch struct{}

func (tw *Twitch) Resolve(arg string) (*Metadata, error) {
	// TODO request the twitch api to see if the user is even online and return a pleasant error
	// TODO request the twitch api to learn the title of the user's broadcast
	md := &Metadata{
		Title:    arg,
		Duration: 0,
		Open:     opener(arg),
	}
	return md, nil
}

func opener(arg string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		streamlink := exec.Command(
			"streamlink",
			"-O",
			arg,
			"audio_only",
		)
		stdout, err := streamlink.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := streamlink.Start(); err != nil {
			return nil, err
		}
		log.Printf("started streamlink targetting %v", arg)
		return twitchReadCloser{stdout, streamlink}, nil
	}
}

type twitchReadCloser struct {
	io.Reader
	streamlink *exec.Cmd
}

func (twrc twitchReadCloser) Close() error {
	err := twrc.streamlink.Process.Kill()
	log.Printf("killed streamlink %v", err)
	err = twrc.streamlink.Wait()
	log.Printf("closed streamlink %v", err)
	return err
}
