package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

var endpointSc = "http://api.soundcloud.com/"
var endpointScResolve = endpointSc + "resolve/"

type Soundcloud struct {
	ClientID string
}

func (sc *Soundcloud) Resolve(arg string) (*Metadata, error) {
	if sc.ClientID == "" {
		return nil, errors.New("no soundcloud client id set up")
	}

	query := url.Values{}
	query.Add("client_id", sc.ClientID)
	query.Add("url", arg)

	resp, err := http.Get(endpointScResolve + "?" + query.Encode())
	log.Printf("resp %#v", resp)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	if resp.ContentLength == 0 {
		return nil, errors.New("no content")
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
		return nil, err
	}
	log.Printf("track info %#v", respJSON)

	dlUrl := ""
	if respJSON.Downloadable {
		dlUrl = respJSON.DownloadURL
	} else if respJSON.Streamable {
		dlUrl = respJSON.StreamURL
	}
	if dlUrl == "" {
		return nil, errors.New("couldn't get a download url")
	}

	query = url.Values{}
	query.Add("client_id", sc.ClientID)
	md := &Metadata{
		Title:    respJSON.Title,
		Duration: time.Duration(respJSON.Duration) * time.Millisecond,
		Open: func() (io.ReadCloser, error) {
			resp, err := http.Get(dlUrl + "?" + query.Encode())
			return resp.Body, err
		},
	}
	return md, nil
}
