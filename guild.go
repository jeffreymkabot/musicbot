package music

import (
	"errors"
	"fmt"
	"log"
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

type guildListener struct {
	events chan<- GuildEvent
	wg     sync.WaitGroup
	closed chan struct{}
}

func (svc *guildListener) Send(evt GuildEvent) error {
	select {
	case svc.events <- evt:
	case <-svc.closed:
		return ErrGuildServiceClosed
	case <-time.After(1 * time.Second):
		return ErrGuildServiceTimeout
	}
	return nil
}

func (svc *guildListener) Close() error {
	select {
	case <-svc.closed:
		return ErrGuildServiceClosed
	default:
	}
	close(svc.closed)
	close(svc.events)
	svc.wg.Wait()
	return nil
}

type guildService struct {
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
) *guildListener {
	eventChan := make(chan GuildEvent)
	listener := &guildListener{
		events: eventChan,
		wg:     sync.WaitGroup{},
		closed: make(chan struct{}),
	}

	// initialize guild service in a separate goroutine
	// so that the Guild function does not block while the guild service initializes
	// hold the mutex on the bot's guildServices map for as short a time as possible

	// listener will wait for the guild service to close its resources
	listener.wg.Add(1)

	go func(events <-chan GuildEvent) {
		info, err := store.Get(guild.ID)
		if err != nil {
			info = defaultGuildInfo
			info.MusicChannel = detectMusicChannel(guild)
			store.Put(guild.ID, info)
		}
		gsvc := &guildService{
			GuildInfo:    info,
			guildID:      guild.ID,
			guildOwnerID: guild.OwnerID,
			discord:      discord,
			store:        store,
			player:       openPlayer(info.MusicChannel),
			commands:     commands,
			plugins:      plugins,
		}
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
		listener.wg.Done()
	}(eventChan)

	return listener
}

// act only on messages beginning with an appropriate prefix in an appropriate channel by an appropriate user
// match message against commands, then against plugins if no match against commands
func (gsvc *guildService) handleMessageEvent(evt GuildEvent) {
	prefix := ""
	if strings.HasPrefix(evt.Body, gsvc.Prefix) {
		prefix = gsvc.Prefix
	} else if strings.HasPrefix(evt.Body, defaultCommandPrefix) {
		prefix = defaultCommandPrefix
	}
	if prefix == "" {
		return
	}

	arg := strings.TrimSpace(strings.TrimPrefix(evt.Body, prefix))

	cmd, argv, cmdOK := matchCommand(gsvc.commands, arg)
	if cmdOK {
		if gsvc.isAllowed(cmd, evt) {
			log.Printf("evt %v -> command %v", evt, cmd.name)
			gsvc.runAndRespondToMessage(cmd.run, evt, argv, cmd.ack)
		}
		return
	}

	// query plugins, but validate event first to fail fast
	if !gsvc.isAllowed(command{restrictChannel: true}, evt) {
		return
	}

	fn, pluginOK := matchPlugin(gsvc.plugins, arg)
	if !pluginOK {
		return
	}

	log.Printf("evt %v -> plugin %v", evt, arg)
	gsvc.runAndRespondToMessage(fn, evt, nil, requeue.ack)
}

func (gsvc *guildService) isAllowed(cmd command, evt GuildEvent) bool {
	channelOK := !cmd.restrictChannel || contains(gsvc.ListenChannels, evt.Channel.ID)
	authorOK := !cmd.ownerOnly || evt.Message.Author.ID == gsvc.guildOwnerID
	return channelOK && authorOK
}

func (gsvc *guildService) runAndRespondToMessage(fn serviceFunc, evt GuildEvent, args []string, ack string) {
	err := fn(gsvc, evt, args)
	// error response
	if err != nil {
		gsvc.discord.ChannelMessageSend(evt.Channel.ID, fmt.Sprintf("🤔...\n%v", err))
		return
	}
	// success ack
	if ack != "" {
		gsvc.discord.MessageReactionAdd(evt.Channel.ID, evt.Message.ID, ack)
	}
}

// check if reaction is invocation of cmd shortcut on the player status message
// otherwise check if it is a requeue reaction to a previously queued song
func (gsvc *guildService) handleReactionEvent(evt GuildEvent) {
	nowPlaying, ok := gsvc.player.NowPlaying()
	if ok && evt.Channel.ID == nowPlaying.statusMessageChannelID && evt.Message.ID == nowPlaying.statusMessageID {
		for _, cmd := range gsvc.commands {
			if cmd.shortcut == evt.Body {
				// no error response or success ack
				_ = cmd.run(gsvc, evt, []string{})
				return
			}
		}
	}

	if requeue.shortcut != evt.Body {
		return
	}

	// react event does not have full message struct, try to recover the message
	msg, err := gsvc.discord.State.Message(evt.Channel.ID, evt.Message.ID)
	if err != nil {
		msg, err = gsvc.discord.ChannelMessage(evt.Channel.ID, evt.Message.ID)
		if err != nil {
			return
		}
	}

	if requeueable(msg) {
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

// Message is requeueable if I reacted to it with the requeue command shortcut
func requeueable(msg *discordgo.Message) bool {
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

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}
