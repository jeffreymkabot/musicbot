package plugins

import (
	"net/url"
	"regexp"
)

var urlRegexpTw = regexp.MustCompile(`twitch\.tv`)

type Twitch struct{
	Streamlink
}

func (tw Twitch) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpTw.MatchString(url.Hostname())
}

func (tw Twitch) Resolve(arg string) (md Metadata, err error) {
	// TODO request the twitch api to see if the user is even online and return a pleasant error
	// also request the twitch api to learn the title of the user's broadcast
	return tw.Streamlink.Resolve(arg)
}

func (tw Twitch) ResolveWithVideo(arg string) (md Metadata, err error) {
	// TODO request the twitch api to see if the user is even online and return a pleasant error
	// also request the twitch api to learn the title of the user's broadcast
	return tw.Streamlink.ResolveWithVideo(arg)
}
