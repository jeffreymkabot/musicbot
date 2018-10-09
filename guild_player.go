package musicbot

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/discordvoice/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
	"github.com/jonas747/dca"
)

// ErrInvalidMusicChannel is emitted when the music channel configured for a guild is not a discord voice channel.
var ErrInvalidMusicChannel = errors.New("set a valid voice channel for music playback, then call reconnect")

var encodeOptions = dca.EncodeOptions{
	Volume:           256,
	Channels:         2,
	FrameRate:        48000,
	FrameDuration:    20,
	Bitrate:          128,
	RawOutput:        false,
	Application:      dca.AudioApplicationAudio,
	CompressionLevel: 10,
	PacketLoss:       1,
	BufferedFrames:   100,
	VBR:              false,
	AudioFilter:      "",
}

// GuildPlayer streams audio to a voice channel in a guild.
type GuildPlayer interface {
	Put(evt MessageEvent, voiceChannelID string, md plugins.Metadata, loudness float64) error
	Skip()
	Pause()
	Clear()
	Close() error
	NowPlaying() (Play, bool)
	Playlist() []string
}

// Play holds data related to the playback of an audio stream in a guild.
type Play struct {
	plugins.Metadata
	StatusMessageChannelID string
	StatusMessageID        string
}

type guildPlayer struct {
	guildID string
	discord *discordgo.Session
	device  *discordvoice.Device
	*player.Player
	cmdShortcuts []string
	mu           sync.Mutex
	// TODO how to manage nowPlaying state in a reasonable way without mutex?
	// player state controlled by discordvoice#sender goroutine
	// guild state controlled by musicbot#Guild goroutine
	nowPlaying Play
}

// NewGuildPlayer creates a GuildPlayer resource for a discord guild.
// Existing open GuildPlayers for the same guild should be closed before making a new one to avoid interference.
func NewGuildPlayer(guildID string, discord *discordgo.Session, idleChannelID string, cmdShortcuts []string) GuildPlayer {
	idle := func() {
		if discordvoice.ValidVoiceChannel(discord, idleChannelID) {
			discord.ChannelVoiceJoin(guildID, idleChannelID, false, true)
		}
	}
	return &guildPlayer{
		guildID: guildID,
		discord: discord,
		device:  discordvoice.New(discord, guildID, 150*time.Millisecond),
		Player: player.New(
			player.QueueLength(10),
			player.IdleFunc(idle, 1000),
		),
		cmdShortcuts: cmdShortcuts,
	}
}

func (gp *guildPlayer) Put(evt MessageEvent, voiceChannelID string, md plugins.Metadata, loudness float64) error {
	if !discordvoice.ValidVoiceChannel(gp.discord, voiceChannelID) {
		return ErrInvalidMusicChannel
	}

	log.Printf("put %v", md.Title)

	statusChannelID, statusMessageID := evt.Message.ChannelID, ""
	statusEmbed := statusEmbedFunc()
	updateStatus := updateStatusFunc(gp, md, statusChannelID)

	return gp.Enqueue(
		md.Title,
		openSrcFunc(md.OpenFunc, loudness),
		openDstFunc(gp.device, voiceChannelID),
		player.Duration(md.Duration),
		player.OnStart(func() {
			embed := statusEmbed(embedConfig{md.Title, true, md.Duration, 0, nil, gp.Playlist()})
			statusMessageID = updateStatus(embed, statusMessageID)
		}),
		player.OnPause(func(d time.Duration) {
			embed := statusEmbed(embedConfig{md.Title, false, md.Duration, d, nil, gp.Playlist()})
			statusMessageID = updateStatus(embed, statusMessageID)
		}),
		player.OnResume(func(d time.Duration) {
			embed := statusEmbed(embedConfig{md.Title, true, md.Duration, d, nil, gp.Playlist()})
			statusMessageID = updateStatus(embed, statusMessageID)
		}),
		player.OnProgress(
			func(d time.Duration, frameTimes []time.Duration) {
				embed := statusEmbed(embedConfig{md.Title, true, md.Duration, d, frameTimes, gp.Playlist()})
				statusMessageID = updateStatus(embed, statusMessageID)
			},
			5*time.Second,
		),
		player.OnEnd(func(d time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", d, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			if statusMessageID != "" {
				gp.discord.ChannelMessageDelete(statusChannelID, statusMessageID)
				gp.mu.Lock()
				gp.nowPlaying = Play{}
				gp.mu.Unlock()
			}
			gp.discord.MessageReactionAdd(evt.Message.ChannelID, evt.Message.ID, requeue.shortcut)
		}),
	)
}

func openSrcFunc(openFunc func() (io.ReadCloser, error), loudness float64) player.SourceOpenerFunc {
	return func() (player.Source, error) {
		rc, err := openFunc()
		if err != nil {
			return nil, err
		}
		opts := encodeOptions
		if loudness >= 70.0 && loudness <= -5.0 {
			opts.AudioFilter = fmt.Sprintf("loudnorm=i=%.1f", loudness)
		}
		return discordvoice.NewSource(rc, &opts)
	}
}

func openDstFunc(device *discordvoice.Device, channelID string) player.DeviceOpenerFunc {
	return func() (io.Writer, error) {
		return device.Open(channelID)
	}
}

type embedConfig struct {
	title      string
	playing    bool
	duration   time.Duration
	elapsed    time.Duration
	frameTimes []time.Duration
	playlist   []string
}

func statusEmbedFunc() func(cfg embedConfig) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Color:  0xa680ee,
		Footer: &discordgo.MessageEmbedFooter{},
	}
	return func(cfg embedConfig) *discordgo.MessageEmbed {
		playPaused := "▶️"
		if !cfg.playing {
			playPaused = "⏸️"
		}
		embed.Title = playPaused + " " + cfg.title
		embed.Description = prettyTime(cfg.elapsed) + "/" + prettyTime(cfg.duration)
		if len(cfg.playlist) > 0 {
			embed.Fields = []*discordgo.MessageEmbedField{
				&discordgo.MessageEmbedField{
					Name:  "Playlist",
					Value: strings.Join(cfg.playlist, "\n"),
				},
			}
		}
		if len(cfg.frameTimes) > 0 {
			avg, dev, max, min := statistics(latenciesAsFloat(cfg.frameTimes))
			embed.Footer.Text = fmt.Sprintf("avg %.3fms, dev %.3fms, max %.3fms, min %.3fms", avg, dev, max, min)
		}
		return embed
	}
}

func updateStatusFunc(gp *guildPlayer, md plugins.Metadata, channelID string) func(*discordgo.MessageEmbed, string) string {
	return func(embed *discordgo.MessageEmbed, messageID string) string {
		if messageID == "" {
			msg, err := gp.discord.ChannelMessageSendEmbed(channelID, embed)
			if err != nil {
				log.Printf("failed to display player status %v", err)
				return ""
			}

			gp.mu.Lock()
			gp.nowPlaying = Play{
				Metadata:               md,
				StatusMessageChannelID: msg.ChannelID,
				StatusMessageID:        msg.ID,
			}
			gp.mu.Unlock()

			for _, emoji := range gp.cmdShortcuts {
				if err := gp.discord.MessageReactionAdd(channelID, msg.ID, emoji); err != nil {
					log.Printf("failed to attach cmd shortcut to player status %v", err)
				}
			}
			return msg.ID
		}

		// edit message in separate goroutine so the edit http request does not  interfere with audio playback
		go func() {
			_, err := gp.discord.ChannelMessageEditEmbed(channelID, messageID, embed)
			if err != nil {
				log.Printf("failed to refresh player status %v", err)
			}
		}()
		return messageID
	}
}

func (gp *guildPlayer) NowPlaying() (play Play, ok bool) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if gp.nowPlaying.StatusMessageID == "" {
		return Play{}, false
	}
	return gp.nowPlaying, true
}

func prettyTime(t time.Duration) string {
	hours := int(t.Hours())
	min := int(t.Minutes()) % 60
	sec := int(t.Seconds()) % 60
	if hours >= 1 {
		return fmt.Sprintf("%02v:%02v:%02v", hours, min, sec)
	}
	return fmt.Sprintf("%02v:%02v", min, sec)
}

// frame-to-frame latency in milliseconds
func latenciesAsFloat(ftf []time.Duration) []float64 {
	latencies := make([]float64, len(ftf))
	for idx, f := range ftf {
		latencies[idx] = float64(f.Nanoseconds()) / 1e6
	}
	return latencies
}

func statistics(data []float64) (avg float64, dev float64, max float64, min float64) {
	if len(data) == 0 {
		return
	}
	min = math.MaxFloat64
	sum := 0.0
	for _, v := range data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg = sum / float64(len(data))
	for _, v := range data {
		dev += ((v - avg) * (v - avg))
	}
	dev = dev / float64(len(data))
	dev = math.Sqrt(dev)
	return
}
