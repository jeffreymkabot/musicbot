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

var ErrGuildServiceTimeout = errors.New("service timed out")
var ErrGuildServiceClosed = errors.New("service is disposed")

// GuildService handles incoming GuildEvents.
// Close is idempotent.
type GuildService interface {
	Send(GuildEvent) error
	Close()
}

// GuildEvent provides instructions to a GuildService.
type GuildEvent struct {
	Type    GuildEventType
	Channel discordgo.Channel
	Message discordgo.Message
	Author  discordgo.User
	Body    string
}

type GuildEventType int

const (
	MessageEvent GuildEventType = iota
	ReactEvent
)

type syncGuildService struct {
	eventChan chan<- GuildEvent
	wg        sync.WaitGroup
	closed    chan struct{}
}

func (svc *syncGuildService) Send(evt GuildEvent) error {
	select {
	case svc.eventChan <- evt:
	case <-svc.closed:
		return ErrGuildServiceClosed
	case <-time.After(1 * time.Second):
		return ErrGuildServiceTimeout
	}
	return nil
}

func (svc *syncGuildService) Close() {
	select {
	case <-svc.closed:
	default:
		close(svc.closed)
		close(svc.eventChan)
		svc.wg.Wait()
	}
}

type guildService struct {
	syncGuildService
	GuildInfo
	guildID string
	// TODO soundcloud client id
	soundcloud string
	store      GuildStorage
	discord    *discordgo.Session
	player     *dcv.Player
	// TODO how to manage statusMessage state in a reasonable way without mutex?
	mu                  sync.Mutex
	playerStatusMessage *discordgo.Message
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

// Guild creates a new GuildService.
// The service returned is safe to use in multiple threads.
func Guild(session *discordgo.Session, guild *discordgo.Guild, store GuildStorage) GuildService {
	gsvc := guildService{
		guildID: guild.ID,
		store:   store,
		discord: session,
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

	eventChan := make(chan GuildEvent)
	gsvc.syncGuildService = syncGuildService{
		eventChan: eventChan,
		wg:        sync.WaitGroup{},
		closed:    make(chan struct{}),
	}

	gsvc.wg.Add(1)
	go func() {
		for evt := range eventChan {
			switch evt.Type {
			case MessageEvent:
				gsvc.handleMessageEvent(evt)
			case ReactEvent:
				gsvc.handleReactionEvent(evt)
			}
		}
		gsvc.player.Quit()
		gsvc.save()
		gsvc.wg.Done()
	}()

	return &gsvc
}

// act only on messages beginning with an appropriate prefix in an appropriate channel by an appropriate user
func (gsvc *guildService) handleMessageEvent(evt GuildEvent) {
	prefix := ""
	if strings.HasPrefix(evt.Body, gsvc.Prefix) {
		prefix = gsvc.Prefix
	} else if strings.HasPrefix(evt.Body, defaultCommandPrefix) {
		prefix = defaultCommandPrefix
	} else {
		return
	}

	args := strings.Fields(strings.TrimPrefix(evt.Body, prefix))
	cmd, args, ok := parseCommand(args)
	if !ok {
		return
	}

	if cmd.restrictChannel && !contains(gsvc.ListenChannels, evt.Channel.ID) {
		log.Printf("command %s invoked in unregistered channel %s", cmd.name, evt.Channel.ID)
		return
	}

	if cmd.ownerOnly && !isOwner(evt.Message.Author.ID) {
		log.Printf("user %s not allowed to execute command %s", evt.Message.Author.ID, cmd.name)
		return
	}

	err := cmd.run(gsvc, evt, args)
	// write error response or react with success ack
	if err != nil {
		gsvc.discord.ChannelMessageSend(evt.Channel.ID, fmt.Sprintf("🤔...\n%v", err))
		return
	}
	if cmd.ack != "" {
		gsvc.discord.MessageReactionAdd(evt.Channel.ID, evt.Message.ID, cmd.ack)
	}
}

// act only on reactions placed on the status message
func (gsvc *guildService) handleReactionEvent(evt GuildEvent) {
	gsvc.mu.Lock()
	statusChannelID, statusMessageID := gsvc.playerStatusMessage.ChannelID, gsvc.playerStatusMessage.ID
	gsvc.mu.Unlock()
	if evt.Channel.ID == statusChannelID && evt.Message.ID == statusMessageID {
		for _, cmd := range commands {
			// no viable vector for send error response or success ack
			if cmd.shortcut == evt.Body {
				cmd.run(gsvc, evt, nil)
				return
			}
		}
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
			msg, err := gsvc.discord.ChannelMessageSendEmbed(statusChannelID, embed)
			if err != nil {
				log.Printf("failed to display player status %v", err)
				return
			}
			statusMessageID = msg.ID

			// wait for the status message to be deleted when the guildservice closes
			gsvc.wg.Add(1)

			gsvc.mu.Lock()
			gsvc.playerStatusMessage = msg
			gsvc.mu.Unlock()

			for _, cmd := range commands {
				if cmd.shortcut != "" {
					gsvc.discord.MessageReactionAdd(statusChannelID, statusMessageID, cmd.shortcut)
				}
			}
		} else {
			_, err := gsvc.discord.ChannelMessageEditEmbed(statusChannelID, statusMessageID, embed)
			if err != nil {
				log.Printf("failed to refresh player status %v", err)
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
			if statusMessageID != "" {
				gsvc.discord.ChannelMessageDelete(statusChannelID, statusMessageID)
				gsvc.wg.Done()
			}
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
		gsvc.discord,
		gsvc.guildID,
		gsvc.MusicChannel,
		dcv.QueueLength(10),
	)
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
