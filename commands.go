package music

import (
	"time"
	// "bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/boltdb/bolt"
	dgv "github.com/jeffreymkabot/discordvoice"
	"github.com/rylio/ytdl"
)

type command struct {
	name                string
	alias               []string
	usage               string
	short               string
	long                string
	isOwnerOnly         bool
	isListenChannelOnly bool
	run                 func(*Bot, *guild, string, []string) error
}

var help = &command{
	name: "help",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		return nil
	},
}

// TODO youtube / soundcloud / etc implement a common interface

var youtube = &command{
	name:                "youtube",
	alias:               []string{"yt"},
	isListenChannelOnly: true,
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}

		voiceChannelID := guildMusicChannelID(b.session, g.guildID)
		if voiceChannelID == "" {
			return errors.New("no music channel set up")
		}

		resourceUrl := args[0]
		info, err := ytdl.GetVideoInfo(resourceUrl)
		if err != nil {
			return err
		}

		dlUrl, err := info.GetDownloadURL(info.Formats.Extremes(ytdl.FormatAudioEncodingKey, true)[0])
		if err != nil {
			return err
		}
		log.Printf("dl url %s", dlUrl)

		payload := &dgv.Payload{
			ChannelID: voiceChannelID,
			URL:       dlUrl.String(),
			Volume:    64,
			Name:      info.Title,
			Duration:  info.Duration,
		}

		return g.play.Enqueue(payload)
	},
}

// TODO soundcloud this should be split up into smaller functions, probably its own source file

var endpointSc = "http://api.soundcloud.com/"
var endpointScResolve = endpointSc + "resolve/"

var soundcloud = &command{
	name:                "soundcloud",
	alias:               []string{"sc"},
	isListenChannelOnly: true,
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}

		voiceChannelID := guildMusicChannelID(b.session, g.guildID)
		if voiceChannelID == "" {
			return errors.New("no music channel set up")
		}

		if b.soundcloud == "" {
			return errors.New("no soundcloud client id set up")
		}

		resourceUrl := args[0]

		query := url.Values{}
		query.Add("client_id", b.soundcloud)
		query.Add("url", resourceUrl)

		resp, err := http.Get(endpointScResolve + "?" + query.Encode())
		log.Printf("resp %#v", resp)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			return errors.New(resp.Status)
		}
		if resp.ContentLength == 0 {
			return errors.New("no content")
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
			return err
		}
		log.Printf("track info %#v", respJSON)

		dlUrl := ""
		if respJSON.Downloadable {
			dlUrl = respJSON.DownloadURL
		} else if respJSON.Streamable {
			dlUrl = respJSON.StreamURL
		}
		if dlUrl == "" {
			return errors.New("couldn't get a download url")
		}

		query = url.Values{}
		query.Add("client_id", b.soundcloud)

		payload := &dgv.Payload{
			ChannelID: voiceChannelID,
			URL:       dlUrl + "?" + query.Encode(),
			Volume:    64,
			Name:      respJSON.Title,
			Duration:  time.Duration(respJSON.Duration) * time.Millisecond,
		}

		return g.play.Enqueue(payload)
	},
}

var skip = &command{
	name: "skip",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if err := g.play.Skip(); err != nil {
			log.Printf("control was full when tried to send skip")
		} else {
			log.Printf("sent skip")
		}
		return nil
	},
}

var pause = &command{
	name: "pause",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if err := g.play.Pause(); err != nil {
			log.Printf("control was full when tried to send pause")
		} else {
			log.Printf("sent pause")
		}
		return nil
	},
}

var stop = &command{
	name: "stop",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		return nil
	},
}

var setPrefix = &command{
	name: "prefix",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 || args[0] == "" {
			return errors.New("prefix please")
		}
		g.mu.Lock()
		g.Prefix = args[0]
		g.mu.Unlock()
		// db
		return nil
	},
}

var setListen = &command{
	name: "listenhere",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if textChannelID == "" {
			return errors.New("channel please")
		}
		g.mu.Lock()
		if !contains(g.ListenChannels, textChannelID) {
			g.ListenChannels = append(g.ListenChannels, textChannelID)
		}
		g.mu.Unlock()
		// db
		err := b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(g.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(g.guildID), val)
		})
		if err != nil {
			return err
		}
		b.session.ChannelMessageSend(textChannelID, "ok")
		return nil
	},
}

var unsetListen = &command{
	name: "unlistenhere",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if textChannelID == "" {
			return errors.New("channel please")
		}
		g.mu.Lock()
		for i, ch := range g.ListenChannels {
			if ch == textChannelID {
				g.ListenChannels = append(g.ListenChannels[:i], g.ListenChannels[i+1:]...)
			}
		}
		g.mu.Unlock()
		// db
		err := b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(g.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(g.guildID), val)
		})
		if err != nil {
			return err
		}
		b.session.ChannelMessageSend(textChannelID, "ok")
		return nil
	},
}
