package plugins

import (
	"errors"
	"log"
)

type SearchMultiple struct {
	Resources []Plugin
}

func (sm SearchMultiple) CanHandle(arg string) bool {
	return arg != ""
}

func (sm SearchMultiple) Resolve(arg string) (md Metadata, err error) {
	// TODO since search plugins may return real but irrelevant results
	// should evaluate results and return the one most relevant to input arg
	for _, pl := range sm.Resources {
		md, err = pl.Resolve(arg)
		if err == nil {
			return
		}
		log.Printf("search failed %v", err)
	}
	err = errors.New("no results")
	return
}
