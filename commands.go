package music

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/boltdb/bolt"
	dgv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

type command struct {
	name          string
	alias         []string
	usage         string
	short         string
	long          string
	ownerOnly     bool
	listenChannel bool
	ack           string // must be an emoji, used to react on success
	run           func(*Bot, *guild, string, []string) error
}

var help = &command{
	name:  "help",
	alias: []string{"h"},
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		return nil
	},
}

var youtube = &command{
	name:          "youtube",
	alias:         []string{"yt", "youtu"},
	listenChannel: true,
	ack:           "☑",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}

		voiceChannelID := guildMusicChannelID(b.session, g.guildID)
		if voiceChannelID == "" {
			return errors.New("no music channel set up")
		}

		md, err := (&plugins.Youtube{}).DownloadURL(args[0])
		if err != nil {
			return err
		}

		status, err := g.play.Enqueue(voiceChannelID, md.DownloadURL, dgv.Volume(64), dgv.Title(md.Title), dgv.Duration(md.Duration))
		if err != nil {
			return err
		}

		go b.listen(textChannelID, status)
		return nil
	},
}

var soundcloud = &command{
	name:          "soundcloud",
	alias:         []string{"sc", "snd"},
	listenChannel: true,
	ack:           "☑",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}

		voiceChannelID := guildMusicChannelID(b.session, g.guildID)
		if voiceChannelID == "" {
			return errors.New("no music channel set up")
		}

		md, err := (&plugins.Soundcloud{ClientID: b.soundcloud}).DownloadURL(args[0])
		if err != nil {
			return err
		}

		status, err := g.play.Enqueue(voiceChannelID, md.DownloadURL, dgv.Volume(64), dgv.Title(md.Title), dgv.Duration(md.Duration))
		if err != nil {
			return err
		}

		go b.listen(textChannelID, status)
		return nil
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
	name:  "pause",
	alias: []string{"p"},
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if err := g.play.Pause(); err != nil {
			log.Printf("control was full when tried to send pause")
		} else {
			log.Printf("sent pause")
		}
		return nil
	},
}

var clear = &command{
	name:  "clear",
	alias: []string{"cl"},
	ack:   "🔘",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		return g.play.Clear()
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
	ack:  "🆗",
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
		return nil
	},
}

var unsetListen = &command{
	name: "unlistenhere",
	ack:  "🆗",
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
		return nil
	},
}
