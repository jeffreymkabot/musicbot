package plugins

import (
	"net/url"
	"regexp"
)

var urlRegexpTw = regexp.MustCompile(`twitch\.tv`)

type Twitch struct{}

func (tw Twitch) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpTw.MatchString(url.Hostname())
}

func (tw Twitch) Resolve(arg string) (md Metadata, err error) {
	// TODO request the twitch api to see if the user is even online and return a pleasant error
	// also request the twitch api to learn the title of the user's broadcast
	md = Metadata{
		Title:    arg,
		Duration: 0,
		OpenFunc: streamlinkOpener(arg, "audio_only,480p,720p,best"),
	}
	return
}
