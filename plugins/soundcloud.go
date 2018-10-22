package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const endpointSc = "https://api.soundcloud.com/"
const endpointScResolve = endpointSc + "resolve/"

var urlRegexpSc = regexp.MustCompile(`soundcloud\.com`)

type soundcloudTrack struct {
	Downloadable bool
	DownloadURL  string `json:"download_url"`
	Streamable   bool
	StreamURL    string `json:"stream_url"`
	Title        string
	Duration     int
}

type Soundcloud struct {
	ClientID string
}

func (sc Soundcloud) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpSc.MatchString(url.Hostname())
}

func (sc Soundcloud) Resolve(arg string) (md Metadata, err error) {
	if sc.ClientID == "" {
		err = errors.New("no soundcloud client id")
		return
	}

	query := url.Values{}
	query.Add("client_id", sc.ClientID)
	query.Add("url", arg)

	resp, err := http.Get(endpointScResolve + "?" + query.Encode())
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err = errors.New(resp.Status)
		return
	}
	if resp.ContentLength == 0 {
		err = errors.New("no content")
		return
	}

	var track soundcloudTrack
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&track)
	if err != nil {
		return
	}

	return track.Metadata(sc.ClientID)
}

func (sct soundcloudTrack) Metadata(clientID string) (md Metadata, err error) {
	dlUrl := ""
	if sct.Downloadable {
		dlUrl = sct.DownloadURL
	} else if sct.Streamable {
		dlUrl = sct.StreamURL
	}
	if dlUrl == "" {
		err = errors.New("couldn't get a download url")
		return
	}

	query := url.Values{}
	query.Add("client_id", clientID)
	md = Metadata{
		Title:    sct.Title,
		Duration: time.Duration(sct.Duration) * time.Millisecond,
		OpenAudioStream: func() (io.ReadCloser, error) {
			resp, err := http.Get(dlUrl + "?" + query.Encode())
			return resp.Body, err
		},
	}
	return
}
