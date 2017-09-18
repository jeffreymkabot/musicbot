package music

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/boltdb/bolt"
	dgv "github.com/jeffreymkabot/aoebot/discordvoice"
	"github.com/jonas747/dca"
	"github.com/rylio/ytdl"
)

type command struct {
	name                string
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

var encodeOptions = &dca.EncodeOptions{
	Volume:           256,
	Channels:         2,
	FrameRate:        48000,
	FrameDuration:    20,
	Bitrate:          128,
	RawOutput:        true,
	Application:      dca.AudioApplicationAudio,
	CompressionLevel: 10,
	PacketLoss:       1,
	BufferedFrames:   100,
	VBR:              true,
}

type audiosession struct {
	*http.Response
	*dca.EncodeSession
}

func athing(io.Closer) {}

func (p audiosession) Close() error {
	p.Body.Close()
	p.Cleanup()
	return nil
}

var youtube = &command{
	name:                "youtube",
	isListenChannelOnly: true,
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}

		voiceChannelID := guildMusicChannelID(b.session, g.guildID)
		if voiceChannelID == "" {
			return errors.New("no music channel set up")
		}

		url := args[0]
		info, err := ytdl.GetVideoInfo(url)
		if err != nil {
			return err
		}

		dlUrl, err := info.GetDownloadURL(info.Formats.Extremes(ytdl.FormatAudioEncodingKey, true)[0])
		if err != nil {
			return err
		}

		resp, err := http.Get(dlUrl.String())
		if err != nil {
			return err
		}

		encoder, err := dca.EncodeMem(resp.Body, encodeOptions)
		as := audiosession{resp, encoder}
		payload := &dgv.Payload{
			ChannelID: voiceChannelID,
			Reader:    as,
		}
		select {
		case g.send.Queue <- payload:
		default:
			as.Close()
			return errors.New("queue is full for this guild")
		}

		return nil
	},
}

var skip = &command{
	name: "skip",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		// buffered channel so don't wait
		log.Printf("send skip")
		g.send.Skip <- struct{}{}
		log.Printf("Sent skip")
		return nil
	},
}

var pause = &command{
	name: "pause",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
		return nil
	},
}

var unpause = &command{
	name: "unpause",
	run: func(b *Bot, g *guild, textChannelID string, args []string) error {
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
