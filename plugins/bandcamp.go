package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"time"
)

type Bandcamp struct{}

var trackinfoRegexp = regexp.MustCompile(`trackinfo: \[({.*})\]`)

func (bc *Bandcamp) Resolve(arg string) (*Metadata, error) {
	resp, err := http.Get(arg)
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	matches := trackinfoRegexp.FindSubmatch(body)
	if matches == nil || len(matches[1]) == 0 {
		return nil, errors.New("could not find track info")
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
		return nil, err
	}

	log.Printf("track info %#v", trackinfoJson)
	// bandcamp reports duration in seconds
	dur := time.Duration(int(trackinfoJson.Duration*1000)) * time.Millisecond
	md := &Metadata{
		Title:    trackinfoJson.Title,
		Duration: dur,
		Open: func() (io.ReadCloser, error) {
			resp, err := http.Get(trackinfoJson.File.URL)
			return resp.Body, err
		},
	}
	return md, nil
}
