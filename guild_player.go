package musicbot

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"io"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

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
	mu           sync.Mutex // protect statusMessageRef
	// TODO how to manage statusMessageRef state in a reasonable way without mutex?
	// player state controlled by discordvoice#sender goroutine
	// guild state controlled by musicbot#Guild goroutine
	statusMessageRef Play
}

// NewGuildPlayer creates a GuildPlayer resource for a discord guild.
// Existing open GuildPlayers for the same guild should be closed before making a new one to avoid interference.
func NewGuildPlayer(guildID string, discord *discordgo.Session, idleChannelID string, cmdShortcuts []string) GuildPlayer {
	gp := &guildPlayer{
		guildID:      guildID,
		discord:      discord,
		device:       discordvoice.New(discord, guildID, 150*time.Millisecond),
		Player:       nil,
		cmdShortcuts: cmdShortcuts,
	}
	// clear the current status message and join the idle channel
	idle := func() {
		gp.clearStatusMessage()
		if discordvoice.ValidVoiceChannel(discord, idleChannelID) {
			discord.ChannelVoiceJoin(guildID, idleChannelID, false, true)
		}
	}
	gp.Player = player.New(
		player.QueueLength(10),
		player.IdleFunc(idle, 1000),
	)
	return gp
}

func (gp *guildPlayer) Close() error {
	gp.clearStatusMessage()
	return gp.Player.Close()
}

func (gp *guildPlayer) Put(evt MessageEvent, voiceChannelID string, md plugins.Metadata, loudness float64) error {
	if !discordvoice.ValidVoiceChannel(gp.discord, voiceChannelID) {
		return ErrInvalidMusicChannel
	}

	log.Printf("put %v", md.Title)

	// prefer to open separate video stream
	// fall back to audio stream if not supported or error opening video stream
	var videoStream io.ReadCloser
	openSrc := func() (io.ReadCloser, error) {
		if md.OpenAudioVideoStreams == nil {
			return md.OpenAudioStream()
		}

		audio, video, err := md.OpenAudioVideoStreams()
		if err != nil {
			log.Printf("failed to separate video stream %v", err)
			log.Print("falling back to just an audio stream")
			return md.OpenAudioStream()
		}
		log.Printf("%#v", video)
		videoStream = video
		return audio, nil
	}

	statusChannelID := evt.Message.ChannelID
	embed := &discordgo.MessageEmbed{
		Color:  0xa680ee,
		Footer: &discordgo.MessageEmbedFooter{},
	}
	state := embedConfig{
		title:    md.Title,
		duration: md.Duration,
	}
	onStateChange := func(state embedConfig, video io.Reader) {
		state.image = video
		statusEmbed(embed, state)
		gp.updateStatusMessage(statusChannelID, embed, md)
	}

	return gp.Enqueue(
		md.Title,
		openSrcFunc(openSrc, loudness),
		openDstFunc(gp.device, voiceChannelID),
		player.Duration(md.Duration),
		player.OnStart(func() {
			go onStateChange(state, videoStream)
		}),
		player.OnPause(func(elapsed time.Duration) {
			state.paused = true
			state.elapsed = elapsed
			state.playlist = gp.Playlist()
			go onStateChange(state, nil)
		}),
		player.OnResume(func(elapsed time.Duration) {
			state.paused = false
			state.elapsed = elapsed
			state.playlist = gp.Playlist()
			go onStateChange(state, nil)
		}),
		player.OnProgress(
			func(elapsed time.Duration, frameTimes []time.Duration) {
				state.frameTimes = frameTimes
				state.elapsed = elapsed
				state.playlist = gp.Playlist()
				go onStateChange(state, videoStream)
			},
			5*time.Second,
		),
		player.OnEnd(func(elapsed time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", elapsed, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			// clean up video stream if it was opened
			// status message is cleaned up when player's idleFunc is called or when player is closed
			if videoStream != nil {
				log.Print("closing video stream")
				videoStream.Close()
			}
			gp.discord.MessageReactionAdd(evt.Message.ChannelID, evt.Message.ID, requeue.shortcut)
		}),
	)
}

// call this in a separate goroutine than discordvoice#sender since it makes blocking http requests
func (gp *guildPlayer) updateStatusMessage(channelID string, embed *discordgo.MessageEmbed, md plugins.Metadata) error {
	gp.mu.Lock()
	chID, msgID := gp.statusMessageRef.StatusMessageChannelID, gp.statusMessageRef.StatusMessageID
	gp.mu.Unlock()

	exists := chID != "" && msgID != ""
	differentChannel := chID != channelID
	tooFarBack := func() bool {
		msgs, err := gp.discord.ChannelMessages(chID, 1, "", msgID, "")
		return err != nil || len(msgs) > 0
	}
	if exists && (differentChannel || tooFarBack()) {
		gp.clearStatusMessage()
		exists = false
	}

	if !exists {
		msg, err := gp.discord.ChannelMessageSendEmbed(channelID, embed)
		if err != nil {
			log.Printf("failed to display player status %v", err)
			return err
		}

		gp.mu.Lock()
		gp.statusMessageRef = Play{
			Metadata:               md,
			StatusMessageChannelID: msg.ChannelID,
			StatusMessageID:        msg.ID,
		}
		gp.mu.Unlock()

		for _, emoji := range gp.cmdShortcuts {
			if err := gp.discord.MessageReactionAdd(msg.ChannelID, msg.ID, emoji); err != nil {
				log.Printf("failed to attach cmd shortcut to player status %v", err)
			}
		}
	} else {
		msg, err := gp.discord.ChannelMessageEditEmbed(channelID, msgID, embed)
		if err != nil {
			log.Printf("failed to edit player status %v", err)
			return err
		}

		gp.mu.Lock()
		gp.statusMessageRef = Play{
			Metadata:               md,
			StatusMessageChannelID: msg.ChannelID,
			StatusMessageID:        msg.ID,
		}
		gp.mu.Unlock()
	}

	return nil
}

func (gp *guildPlayer) clearStatusMessage() error {
	gp.mu.Lock()
	chID, msgID := gp.statusMessageRef.StatusMessageChannelID, gp.statusMessageRef.StatusMessageID
	gp.statusMessageRef = Play{}
	gp.mu.Unlock()

	if chID == "" || msgID == "" {
		return nil
	}
	return gp.discord.ChannelMessageDelete(chID, msgID)
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
	paused     bool
	duration   time.Duration
	elapsed    time.Duration
	frameTimes []time.Duration
	playlist   []string
	image      io.Reader
}

func statusEmbed(embed *discordgo.MessageEmbed, state embedConfig) *discordgo.MessageEmbed {
	playPaused := "▶️"
	if state.paused {
		playPaused = "⏸️"
	}
	embed.Title = playPaused + " " + state.title

	desc := &strings.Builder{}
	desc.WriteString(prettyTime(state.elapsed) + "/" + prettyTime(state.duration))

	if state.image != nil {
		img, _, err := image.Decode(state.image)
		if err == nil {
			// TODO determine optimal values
			asciiString := ascii(grayscale(resize(img, 30, 15)))
			desc.WriteString("```\n")
			desc.WriteString(asciiString)
			desc.WriteString("\n```")
		} else {
			log.Printf("failed to decode image %v", err)
		}
	}

	embed.Description = desc.String()

	if len(state.playlist) > 0 {
		embed.Fields = []*discordgo.MessageEmbedField{
			&discordgo.MessageEmbedField{
				Name:  "Playlist",
				Value: strings.Join(state.playlist, "\n"),
			},
		}
	}

	if len(state.frameTimes) > 0 {
		if embed.Footer == nil {
			embed.Footer = &discordgo.MessageEmbedFooter{}
		}
		avg, dev, max, min := statistics(latenciesAsFloat(state.frameTimes))
		embed.Footer.Text = fmt.Sprintf("avg %.3fms, dev %.3fms, max %.3fms, min %.3fms", avg, dev, max, min)
	}

	return embed
}

func (gp *guildPlayer) NowPlaying() (play Play, ok bool) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if gp.statusMessageRef.StatusMessageID == "" {
		return Play{}, false
	}
	return gp.statusMessageRef, true
}

// values taken from golang.org/pkg/image/draw example
// colorset and charset need the same length
var colorset = []color.Color{
	color.Gray{Y: 255},
	color.Gray{Y: 160},
	color.Gray{Y: 70},
	color.Gray{Y: 35},
	color.Gray{Y: 0},
}
var charset = []rune{' ', '░', '▒', '▓', '█'}

// resize the source image
func resize(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// produce a grayscale version of the original image (uses colorset's colors)
func grayscale(src image.Image) *image.Paletted {
	paletteImg := image.NewPaletted(src.Bounds(), colorset)
	draw.FloydSteinberg.Draw(paletteImg, paletteImg.Bounds(), src, image.ZP)
	return paletteImg
}

// produce an ascii representation of a grayscale image (uses charset's chars)
func ascii(img *image.Paletted) string {
	buf := &strings.Builder{}
	width := img.Rect.Bounds().Dx()
	// img.Pix is a flattened slice, where each value is the index of a color in img's palette
	for i, idx := range img.Pix {
		buf.WriteRune(charset[idx])
		// write a newline when next index would be a new row of pixels
		if (i+1)%width == 0 {
			buf.WriteRune('\n')
		}
	}
	return buf.String()
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
