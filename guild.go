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
	"github.com/jeffreymkabot/musicbot/plugins"
)

// ErrGuildServiceTimeout indicates that a guild service has taken too long to accept an event.
var ErrGuildServiceTimeout = errors.New("service timed out")

// ErrGuildServiceClosed indicates that a guild service has been closed.
var ErrGuildServiceClosed = errors.New("service is disposed")

// GuildService handles incoming GuildEvents.
// Send returns an error if the service has been closed or otherwise cannot process the event.
// Close is idempotent, but calls to close after the first may return an error.
type GuildService interface {
	Send(GuildEvent) error
	Close() error
}

// GuildEvent provides instructions to a GuildService.
type GuildEvent struct {
	Type    GuildEventType
	Channel discordgo.Channel
	Message discordgo.Message
	Author  discordgo.User
	Body    string
}

func (evt GuildEvent) String() string {
	return fmt.Sprintf("Guild:%v|Channel:%v|Author:%v|Body:%v",
		evt.Channel.GuildID, evt.Channel.ID, evt.Author.ID, evt.Body)
}

// GuildEventType classifies the source of a GuildEvent.
type GuildEventType int

// GuildEventTypes
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

func (svc *syncGuildService) Close() error {
	select {
	case <-svc.closed:
	default:
		close(svc.closed)
		close(svc.eventChan)
		svc.wg.Wait()
	}
	return nil
}

type guildService struct {
	syncGuildService
	GuildInfo
	guildID      string
	guildOwnerID string
	discord      *discordgo.Session
	store        GuildStorage
	player       GuildPlayer
	commands     []command
	plugins      []plugins.Plugin
}

// GuildStorage is used to persist and retrieve guild configuration.
type GuildStorage interface {
	Get(guildID string) (GuildInfo, error)
	Put(guildID string, info GuildInfo) error
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
// The service returned is safe to use in multiple goroutines.
func Guild(
	guild *discordgo.Guild,
	discord *discordgo.Session,
	store GuildStorage,
	openPlayer func(idleChannelID string) GuildPlayer,
	commands []command,
	plugins []plugins.Plugin,
) GuildService {
	info, err := store.Get(guild.ID)
	if err != nil {
		info = defaultGuildInfo
		info.MusicChannel = detectMusicChannel(guild)
		store.Put(guild.ID, info)
	}

	eventChan := make(chan GuildEvent)
	gsvc := &guildService{
		syncGuildService: syncGuildService{
			eventChan: eventChan,
			wg:        sync.WaitGroup{},
			closed:    make(chan struct{}),
		},
		GuildInfo:    info,
		guildID:      guild.ID,
		guildOwnerID: guild.OwnerID,
		discord:      discord,
		store:        store,
		player:       openPlayer(info.MusicChannel),
		commands:     commands,
		plugins:      plugins,
	}

	// guild users will have to correct the playback channel configuration

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
		gsvc.player.Close()
		gsvc.store.Put(gsvc.guildID, gsvc.GuildInfo)
		gsvc.wg.Done()
	}()

	return gsvc
}

// act only on messages beginning with an appropriate prefix in an appropriate channel by an appropriate user
func (gsvc *guildService) handleMessageEvent(evt GuildEvent) {
	trimTarget := ""
	if strings.HasPrefix(evt.Body, gsvc.Prefix) {
		trimTarget = gsvc.Prefix
	} else if strings.HasPrefix(evt.Body, defaultCommandPrefix) {
		trimTarget = defaultCommandPrefix
	} else {
		return
	}

	args := strings.Fields(strings.TrimPrefix(evt.Body, trimTarget))

	cmd, args, cmdOK := matchCommand(gsvc.commands, args)
	if !cmdOK {
		// synthesize a command function for a matching plugin, if event is valid
		cmd = command{
			restrictChannel: true,
			ownerOnly:       false,
			ack:             "☑",
			run: func(gsvc *guildService, evt GuildEvent, args []string) error {
				return nil
			},
		}
	}
	if !gsvc.isAllowed(cmd, evt) {
		return
	}
	// query plugins _after_ validating the event in order to fail fast
	// since querying some plugins can be slow
	if !cmdOK {
		plugin, pluginOK := matchPlugin(gsvc.plugins, args)
		if !pluginOK {
			return
		}
		cmd.name = args[0]
		cmd.run = runPlugin(plugin)
	}

	log.Printf("evt %v -> command %v", evt, cmd.name)

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

func (gsvc *guildService) isAllowed(cmd command, evt GuildEvent) bool {
	channelOK := !cmd.restrictChannel || contains(gsvc.ListenChannels, evt.Channel.ID)
	authorOK := !cmd.ownerOnly || evt.Message.Author.ID == gsvc.guildOwnerID
	return channelOK && authorOK
}

func (gsvc *guildService) handleReactionEvent(evt GuildEvent) {
	// first check if reaction is to the player status message
	// otherwise check if it is a reaction to a previously queued song
	nowPlaying, ok := gsvc.player.NowPlaying()
	if ok && evt.Channel.ID == nowPlaying.statusMessageChannelID && evt.Message.ID == nowPlaying.statusMessageID {
		for _, cmd := range gsvc.commands {
			// no viable vector for send error response or success ack
			if cmd.shortcut == evt.Body {
				cmd.run(gsvc, evt, []string{})
				return
			}
		}
	}
	// react event does not have full message struct
	// only able to look back at so many messages, so false negatives are possible
	msg, err := gsvc.discord.State.Message(evt.Channel.ID, evt.Message.ID)
	if err != nil {
		return
	}
	if requeueable(*msg) {
		gsvc.handleMessageEvent(GuildEvent{
			Type:    MessageEvent,
			Channel: evt.Channel,
			Message: *msg,
			Author:  *msg.Author,
			Body:    msg.Content,
		})
	}
}

func detectMusicChannel(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), defaultMusicChannelPrefix) {
			return ch.ID
		}
	}
	return ""
}

func detectUserVoiceChannel(g *discordgo.Guild, userID string) string {
	for _, vs := range g.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID
		}
	}
	return ""
}

// requeable if not created by me and I reacted to it with the requeue shortcut
// tolerate false negatives
func requeueable(msg discordgo.Message) bool {
	if msg.Author.Bot {
		return false
	}
	for _, rxn := range msg.Reactions {
		if rxn.Me && rxn.Emoji.Name == requeue.shortcut {
			return true
		}
	}
	return false
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
