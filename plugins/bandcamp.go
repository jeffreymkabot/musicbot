package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var urlRegexpBc = regexp.MustCompile(`bandcamp\.com`)
var trackinfoRegexp = regexp.MustCompile(`trackinfo: \[({.*})\]`)

type Bandcamp struct{}

func (bc Bandcamp) CanHandle(arg string) bool {
	url, err := url.Parse(arg)
	return err == nil && url.IsAbs() && urlRegexpBc.MatchString(url.Hostname())
}

func (bc Bandcamp) Resolve(arg string) (md Metadata, err error) {
	resp, err := http.Get(arg)
	if err != nil {
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}

	matches := trackinfoRegexp.FindSubmatch(body)
	if matches == nil || len(matches[1]) == 0 {
		err = errors.New("could not find track info")
		return
	}

	var trackinfoJson struct {
		Title    string
		Duration float64
		File     struct {
			URL string `json:"mp3-128"`
		}
	}
	err = json.Unmarshal(matches[1], &trackinfoJson)
	if err != nil {
		return
	}

	log.Printf("track info %#v", trackinfoJson)
	// bandcamp reports duration in seconds
	dur := time.Duration(int(trackinfoJson.Duration*1000)) * time.Millisecond
	md = Metadata{
		Title:    trackinfoJson.Title,
		Duration: dur,
		OpenAudioStream: func() (io.ReadCloser, error) {
			resp, err := http.Get(trackinfoJson.File.URL)
			return resp.Body, err
		},
	}
	return
}
