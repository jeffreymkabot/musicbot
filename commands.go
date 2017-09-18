package music

import (
	"errors"
	"net/http"

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
		g.queue <- &dgv.Payload{
			ChannelID: voiceChannelID,
			Reader:    encoder,
		}

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
		g.ListenChannels = append(g.ListenChannels, textChannelID)
		g.mu.Unlock()
		// db
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
		return nil
	},
}
