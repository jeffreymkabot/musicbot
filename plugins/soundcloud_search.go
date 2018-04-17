package plugins

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

const endpointScTracks = endpointSc + "tracks/"

type SoundcloudSearch struct {
	ClientID string
}

func (scs SoundcloudSearch) CanHandle(arg string) bool {
	return arg != "" && !httpRegexp.MatchString(arg)
}

func (scs SoundcloudSearch) Resolve(arg string) (md Metadata, err error) {
	if scs.ClientID == "" {
		err = errors.New("no soundcloud client id")
		return
	}

	query := url.Values{}
	query.Add("client_id", scs.ClientID)
	query.Add("q", arg)

	resp, err := http.Get(endpointScTracks + "?" + query.Encode())
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err = errors.New(resp.Status)
		return
	}

	var tracks []soundcloudTrack
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&tracks)
	if err != nil {
		return
	}

	if len(tracks) == 0 {
		err = errors.New("no results")
		return
	}

	return tracks[0].Metadata(scs.ClientID)
}
