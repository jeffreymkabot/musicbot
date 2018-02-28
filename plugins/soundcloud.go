package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const endpointSc = "http://api.soundcloud.com/"
const endpointScResolve = endpointSc + "resolve/"

var urlRegexpSc = regexp.MustCompile(`soundcloud\.com`)

type Soundcloud struct {
	ClientID string
}

func (sc Soundcloud) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpSc.MatchString(url.Hostname())
}

func (sc Soundcloud) Resolve(arg string) (md Metadata, err error) {
	if sc.ClientID == "" {
		err = errors.New("no soundcloud client id set up")
		return
	}

	query := url.Values{}
	query.Add("client_id", sc.ClientID)
	query.Add("url", arg)

	resp, err := http.Get(endpointScResolve + "?" + query.Encode())
	log.Printf("resp %#v", resp)
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

	var respJSON struct {
		Downloadable bool
		DownloadURL  string `json:"download_url"`
		Streamable   bool
		StreamURL    string `json:"stream_url"`
		Title        string
		Duration     int
	}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&respJSON)
	if err != nil {
		return
	}
	log.Printf("track info %#v", respJSON)

	dlUrl := ""
	if respJSON.Downloadable {
		dlUrl = respJSON.DownloadURL
	} else if respJSON.Streamable {
		dlUrl = respJSON.StreamURL
	}
	if dlUrl == "" {
		err = errors.New("couldn't get a download url")
		return
	}

	query = url.Values{}
	query.Add("client_id", sc.ClientID)
	md = Metadata{
		Title:    respJSON.Title,
		Duration: time.Duration(respJSON.Duration) * time.Millisecond,
		Open: func() (io.ReadCloser, error) {
			resp, err := http.Get(dlUrl + "?" + query.Encode())
			return resp.Body, err
		},
	}
	return
}
