package plugins

type Twitch struct{}

func (tw Twitch) Resolve(arg string) (*Metadata, error) {
	// TODO request the twitch api to see if the user is even online and return a pleasant error
	// also request the twitch api to learn the title of the user's broadcast
	md := &Metadata{
		Title:    arg,
		Duration: 0,
		Open:     streamlinkOpener(arg, "audio_only,480p,720p,best"),
	}
	return md, nil
}
