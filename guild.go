package music

import (
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	dcv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

var ErrGuildClientTimeout = errors.New("request timeout")
var ErrGuildClientClosed = errors.New("client is disposed")

// GuildService handles incoming GuildRequests.
// Close is idempotent.
type GuildService interface {
	Send(GuildRequest) error
	Close()
}

type syncGuildService struct {
	ch     chan<- GuildRequest
	wg     sync.WaitGroup
	closed chan struct{}
}

func (svc *syncGuildService) Send(req GuildRequest) error {
	select {
	case svc.ch <- req:
	case <-svc.closed:
		return ErrGuildClientClosed
	case <-time.After(1 * time.Second):
		return ErrGuildClientTimeout
	}
	return nil
}

func (svc *syncGuildService) Close() {
	select {
	case <-svc.closed:
	default:
		close(svc.closed)
		close(svc.ch)
		svc.wg.Wait()
	}
}

type guildService struct {
	syncGuildService
	GuildInfo
	guildID string
	store   GuildStorage
	session *discordgo.Session
	player  *dcv.Player
}

// GuildStorage is used to persist and retrieve guild configuration.
type GuildStorage interface {
	Read(guildID string, info *GuildInfo) error
	Write(guildID string, info GuildInfo) error
}

// GuildInfo members are persisted using GuildStorage
type GuildInfo struct {
	Prefix         string   `json:"prefix"`
	ListenChannels []string `json:"listen"`
	MusicChannel   string   `json:"play"`
	// Loudness sets the loudness target.  Higher is louder.
	// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
	// Values less than -70.0 or greater than -5.0 have no effect.
	// In particular, the default value of 0 has no effect and audio streams will be unchanged.
	Loudness float64 `json:"loudness"`
}

var defaultGuildInfo = GuildInfo{
	Prefix: defaultCommandPrefix,
}

// GuildRequest provides instructions to a GuildService.
type GuildRequest struct {
	GuildID  string
	Message  *discordgo.Message
	Channel  *discordgo.Channel
	Callback func(err error)
}

// Guild creates a new GuildService.
// The service returned is safe to use in multiple threads.
func Guild(session *discordgo.Session, guild *discordgo.Guild, store GuildStorage) GuildService {
	gsvc := guildService{
		guildID: guild.ID,
		store:   store,
		session: session,
	}

	if err := store.Read(guild.ID, &gsvc.GuildInfo); err != nil {
		gsvc.GuildInfo = defaultGuildInfo
		gsvc.MusicChannel = detectMusicChannel(guild)
	}

	idleChannel := guild.AfkChannelID
	if gsvc.MusicChannel != "" {
		idleChannel = gsvc.MusicChannel
	}

	gsvc.player = dcv.Connect(
		session,
		guild.ID,
		idleChannel,
		dcv.QueueLength(10),
	)

	ch := make(chan GuildRequest)
	gsvc.syncGuildService = syncGuildService{ch, sync.WaitGroup{}, make(chan struct{})}
	gsvc.wg.Add(1)
	go func() {
		for request := range ch {
			gsvc.handle(request)
		}
		gsvc.player.Quit()
		gsvc.wg.Done()
	}()
	return &gsvc
}

func (gsvc *guildService) handle(req GuildRequest) {
	if !strings.HasPrefix(req.Message.Content, gsvc.Prefix) && !strings.HasPrefix(req.Message.Content, defaultCommandPrefix) {
		return
	}

	args := strings.Fields(strings.TrimPrefix(req.Message.Content, gsvc.Prefix))
	cmd, args := parseCommand(args)
	if cmd == nil {
		return
	}

	if cmd.restrictChannel && !contains(gsvc.ListenChannels, req.Channel.ID) {
		log.Printf("command %s invoked in unregistered channel %s", cmd.name, req.Channel.ID)
		return
	}

	if cmd.ownerOnly && !isOwner(req.Message.Author.ID) {
		log.Printf("user %s not allowed to execute command %s", req.Message.Author.ID, cmd.name)
		return
	}

	err := cmd.run(gsvc, req, args)
	if err == nil && cmd.ack != "" {
		gsvc.session.MessageReactionAdd(req.Channel.ID, req.Message.ID, cmd.ack)
	}
	if req.Callback != nil {
		req.Callback(err)
	}
}

func (gsvc *guildService) enqueue(p plugins.Plugin, arg string, statusChannelID string) error {
	musicChannelID := gsvc.MusicChannel
	if musicChannelID == "" {
		return errors.New("no music channel set up")
	}

	md, err := p.Resolve(arg)
	if err != nil {
		return err
	}

	statusMessageID := ""
	embed := &discordgo.MessageEmbed{}
	embed.Color = 0xa680ee
	embed.Footer = &discordgo.MessageEmbedFooter{}
	refreshStatus := func(playing bool, elapsed time.Duration, next string) {
		if playing {
			embed.Title = "▶️ " + md.Title
		} else {
			embed.Title = "⏸️ " + md.Title
		}
		embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
		if next != "" {
			embed.Footer.Text = "On Deck: " + next
		}

		if statusMessageID == "" {
			msg, err := gsvc.session.ChannelMessageSendEmbed(statusChannelID, embed)
			if err != nil {
				log.Printf("failed to display player status", err)
				return
			}
			statusMessageID = msg.ID
			// wait for the status message to be deleted when the guildservice closes
			gsvc.wg.Add(1)
			gsvc.session.MessageReactionAdd(statusChannelID, statusMessageID, pauseCmdEmoji)
			gsvc.session.MessageReactionAdd(statusChannelID, statusMessageID, skipCmdEmoji)
		} else {
			_, err := gsvc.session.ChannelMessageEditEmbed(statusChannelID, statusMessageID, embed)
			if err != nil {
				log.Printf("failed to refresh player status", err)
			}
		}
	}

	return gsvc.player.Enqueue(
		musicChannelID,
		md.Title,
		md.Open,
		dcv.Duration(md.Duration),
		dcv.Loudness(gsvc.Loudness),
		dcv.OnStart(func() { refreshStatus(true, 0, gsvc.player.Next()) }),
		dcv.OnPause(func(d time.Duration) { refreshStatus(false, d, gsvc.player.Next()) }),
		dcv.OnResume(func(d time.Duration) { refreshStatus(true, d, gsvc.player.Next()) }),
		dcv.OnProgress(
			func(d time.Duration, frames []time.Time) {
				avg, dev, max, min := statistics(latencies(frames))
				embed.Fields = []*discordgo.MessageEmbedField{
					&discordgo.MessageEmbedField{
						Name:  "Debug",
						Value: fmt.Sprintf("`avg %.3fms`, `dev %.3fms`, `max %.3fms`, `min %.3fms`", avg, dev, max, min),
					},
				}
				refreshStatus(true, d, gsvc.player.Next())
			},
			5*time.Second,
		),
		dcv.OnEnd(func(d time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", d, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			gsvc.session.ChannelMessageDelete(statusChannelID, statusMessageID)
			gsvc.wg.Done()
		}),
	)
}

func (gsvc *guildService) save() error {
	return gsvc.store.Write(gsvc.guildID, gsvc.GuildInfo)
}

func (gsvc *guildService) reconnect() {
	// TODO make sure discordvoice.player.Quit waits for the sendSong goroutine to finish
	gsvc.player.Quit()
	gsvc.player = dcv.Connect(
		gsvc.session,
		gsvc.guildID,
		gsvc.MusicChannel,
		dcv.QueueLength(10),
	)
}

// frame-to-frame latency in milliseconds
func latencies(times []time.Time) []float64 {
	latencies := make([]float64, len(times)-1)
	for i := 1; i < len(times); i++ {
		latencies[i-1] = float64(times[i].Sub(times[i-1]).Nanoseconds()) / 1e6
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

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}

// TODO
func isOwner(userID string) bool {
	return false
}
