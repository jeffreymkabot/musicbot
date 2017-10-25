package music

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/boltdb/bolt"
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
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		return nil
	},
}

var reconnect = &command{
	name:          "reconnect",
	listenChannel: true,
	ack:           "🆗",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		g, err := b.session.State.Guild(gu.guildID)
		if err == nil {
			b.addGuild(g)
		}
		return err
	},
}

var youtube = &command{
	name:          "youtube",
	alias:         []string{"yt", "youtu"},
	listenChannel: true,
	ack:           "☑",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}
		return b.enqueue(gu, &plugins.Youtube{}, args[0], textChannelID)
	},
}

var soundcloud = &command{
	name:          "soundcloud",
	alias:         []string{"sc", "snd"},
	listenChannel: true,
	ack:           "☑",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}
		return b.enqueue(gu, &plugins.Soundcloud{ClientID: b.soundcloud}, args[0], textChannelID)
	},
}

var skip = &command{
	name: "skip",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if err := gu.play.Skip(); err != nil {
			log.Print("nop skip")
		}
		return nil
	},
}

var pause = &command{
	name:  "pause",
	alias: []string{"p"},
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if err := gu.play.Pause(); err != nil {
			log.Print("nop pause")
		}
		return nil
	},
}

var clear = &command{
	name:  "clear",
	alias: []string{"cl"},
	ack:   "🔘",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		return gu.play.Clear()
	},
}

var setPrefix = &command{
	name: "prefix",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if len(args) == 0 || args[0] == "" {
			return errors.New("prefix please")
		}
		gu.mu.Lock()
		gu.Prefix = args[0]
		gu.mu.Unlock()
		// db
		return nil
	},
}

var setListen = &command{
	name: "listenhere",
	ack:  "🆗",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if textChannelID == "" {
			return errors.New("channel please")
		}
		gu.mu.Lock()
		if !contains(gu.ListenChannels, textChannelID) {
			gu.ListenChannels = append(gu.ListenChannels, textChannelID)
		}
		gu.mu.Unlock()
		// db
		return b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(gu.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(gu.guildID), val)
		})
	},
}

var unsetListen = &command{
	name: "unlistenhere",
	ack:  "🆗",
	run: func(b *Bot, gu *guild, textChannelID string, args []string) error {
		if textChannelID == "" {
			return errors.New("channel please")
		}
		gu.mu.Lock()
		for i, ch := range gu.ListenChannels {
			if ch == textChannelID {
				gu.ListenChannels = append(gu.ListenChannels[:i], gu.ListenChannels[i+1:]...)
			}
		}
		gu.mu.Unlock()
		// db
		return b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(gu.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(gu.guildID), val)
		})
	},
}
