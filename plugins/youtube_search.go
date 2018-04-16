package plugins

import (
	"errors"
	"net/http"
	"regexp"

	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

var httpRegexp = regexp.MustCompile(`http(s)?://`)

type YoutubeSearch struct {
	service *youtube.Service
}

func NewYoutubeService(apikey string) (*YoutubeSearch, error) {
	client := &http.Client{
		Transport: &transport.APIKey{Key: apikey},
	}
	svc, err := youtube.New(client)
	if err != nil {
		return nil, err
	}
	return &YoutubeSearch{service: svc}, nil
}

func (yts *YoutubeSearch) CanHandle(arg string) bool {
	return arg != "" && !httpRegexp.MatchString(arg)
}

func (yts *YoutubeSearch) Resolve(arg string) (md Metadata, err error) {
	call := yts.service.Search.List("snippet").
		Type("video").
		MaxResults(1).
		Q(arg)

	resp, err := call.Do()
	if err != nil {
		return
	}

	if len(resp.Items) == 0 {
		err = errors.New("no results")
		return
	}

	return Youtube{}.Resolve(resp.Items[0].Id.VideoId)
}
