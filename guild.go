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

//
const (
	DefaultCommandPrefix      = "#!"
	DefaultMusicChannelPrefix = "music"
)

// DefaultGuildConfig is the starting configuration for a guild.
var DefaultGuildConfig = GuildConfig{
	Prefix: DefaultCommandPrefix,
}

// ErrGuildServiceTimeout indicates that a guild service has taken too long to accept an event.
var ErrGuildServiceTimeout = errors.New("service timed out")

// ErrGuildServiceClosed indicates that a guild service has been closed.
var ErrGuildServiceClosed = errors.New("service is disposed")

// GuildEvent provides instructions to a Guild.
type GuildEvent struct {
	Type      GuildEventType
	GuildID   string
	ChannelID string
	MessageID string
	AuthorID  string
	Body      string
}

func (evt GuildEvent) String() string {
	return fmt.Sprintf("Guild:%v|Channel:%v|Author:%v|Body:%v",
		evt.GuildID, evt.ChannelID, evt.AuthorID, evt.Body)
}

// GuildEventType classifies the source of a GuildEvent.
type GuildEventType int

// GuildEventTypes
const (
	MessageEvent GuildEventType = iota
	ReactEvent
)

// Guild manages incoming GuildEvents.
// Guild is safe to use in multiple goroutines.
type Guild struct {
	events chan<- GuildEvent
	wg     sync.WaitGroup
	closed chan struct{}
}

// Notify passes a GuildEvent to an underlying GuildService.
// Notify returns an error if the service has been closed or takes too long to accept the event.
func (g *Guild) Notify(evt GuildEvent) error {
	select {
	case g.events <- evt:
	case <-g.closed:
		return ErrGuildServiceClosed
	case <-time.After(1 * time.Second):
		return ErrGuildServiceTimeout
	}
	return nil
}

// Close stops the Guild from accepting more events and releases the resources of an underlying GuildService.
// Close is idempotent, but calls to close after the first return an error.
func (g *Guild) Close() error {
	select {
	case <-g.closed:
		return ErrGuildServiceClosed
	default:
	}
	close(g.closed)
	close(g.events)
	g.wg.Wait()
	return nil
}

// GuildService interacts with a discord guild.
type GuildService struct {
	// GuildService and all functions that act on them
	// are designed to be used in just one goroutine.

	GuildConfig
	guildID      string
	guildOwnerID string
	discord      *discordgo.Session
	store        GuildStorage
	player       GuildPlayer
	commands     []command
	plugins      []plugins.Plugin
}

// GuildStorage persists and retrieves guild configuration.
type GuildStorage interface {
	Get(guildID string) (GuildConfig, error)
	Put(guildID string, info GuildConfig) error
}

// GuildConfig controls some behavior of the GuildService.
type GuildConfig struct {
	// musicbot will act only on messages beginning with this prefix or the default prefix.
	Prefix string `json:"prefix"`
	// Some commands require a text channel to be whitelisted before they will run.
	ListenChannels []string `json:"listen"`
	// Use this voice channel to stream music.
	MusicChannel string `json:"play"`
	// Loudness sets the loudness target.  Higher is louder.
	// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
	// Values less than -70.0 or greater than -5.0 have no effect.
	// In particular, the default value of 0 has no effect and audio streams will be unchanged.
	Loudness float64 `json:"loudness"`
}

// NewGuild creates a new Guild with an underlying GuildService that is ready for GuildEvents.
func NewGuild(
	guild *discordgo.Guild,
	discord *discordgo.Session,
	store GuildStorage,
	openPlayer func(idleChannelID string) GuildPlayer,
	commands []command,
	plugins []plugins.Plugin,
) *Guild {
	eventChan := make(chan GuildEvent)
	listener := &Guild{
		events: eventChan,
		wg:     sync.WaitGroup{},
		closed: make(chan struct{}),
	}

	// listener will wait for the guild service to close its resources
	listener.wg.Add(1)

	info, err := store.Get(guild.ID)
	if err != nil {
		info = DefaultGuildConfig
		info.MusicChannel = detectMusicChannel(guild)
		store.Put(guild.ID, info)
	}

	gsvc := &GuildService{
		GuildConfig:  info,
		guildID:      guild.ID,
		guildOwnerID: guild.OwnerID,
		discord:      discord,
		store:        store,
		player:       openPlayer(info.MusicChannel),
		commands:     commands,
		plugins:      plugins,
	}

	go func(events <-chan GuildEvent) {
		for evt := range eventChan {
			switch evt.Type {
			case MessageEvent:
				gsvc.HandleMessageEvent(evt)
			case ReactEvent:
				gsvc.HandleReactEvent(evt)
			}
		}
		gsvc.player.Close()
		gsvc.store.Put(gsvc.guildID, gsvc.GuildConfig)
		listener.wg.Done()
	}(eventChan)

	return listener
}

// HandleMessageEvent may invoke a command or music plugin appropriate to the input.
// Commands are checked for a match before plugins.
// HandleMessageEvent acts only on messages that begin with the configured prefix
// (and if applicable, are in a configured text channel).
func (gsvc *GuildService) HandleMessageEvent(evt GuildEvent) {
	prefix := ""
	if strings.HasPrefix(evt.Body, gsvc.Prefix) {
		prefix = gsvc.Prefix
	} else if strings.HasPrefix(evt.Body, DefaultCommandPrefix) {
		prefix = DefaultCommandPrefix
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

func (gsvc *GuildService) isAllowed(cmd command, evt GuildEvent) bool {
	channelOK := !cmd.restrictChannel || contains(gsvc.ListenChannels, evt.ChannelID)
	authorOK := !cmd.ownerOnly || evt.AuthorID == gsvc.guildOwnerID
	return channelOK && authorOK
}

func (gsvc *GuildService) runAndRespondToMessage(fn serviceFunc, evt GuildEvent, args []string, ack string) {
	err := fn(gsvc, evt, args)
	// error response
	if err != nil {
		gsvc.discord.ChannelMessageSend(evt.ChannelID, fmt.Sprintf("🤔...\n%v", err))
		return
	}
	// success ack
	if ack != "" {
		gsvc.discord.MessageReactionAdd(evt.ChannelID, evt.MessageID, ack)
	}
}

// HandleReactEvent may invoke a command corresponding to the reacted emoji
// if the reaction is to music player's status message or to a previously queued song.
// musicbot puts its own reactions in these locations so users do not have to guess what emojis do what.
func (gsvc *GuildService) HandleReactEvent(evt GuildEvent) {
	nowPlaying, ok := gsvc.player.NowPlaying()
	if ok && evt.ChannelID == nowPlaying.StatusMessageChannelID && evt.MessageID == nowPlaying.StatusMessageID {
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
	msg, err := gsvc.discord.State.Message(evt.ChannelID, evt.MessageID)
	if err != nil {
		msg, err = gsvc.discord.ChannelMessage(evt.ChannelID, evt.MessageID)
		if err != nil {
			return
		}
	}

	if requeueable(msg) {
		// act as though the reacted message event happened again
		gsvc.HandleMessageEvent(GuildEvent{
			Type:      MessageEvent,
			GuildID:   evt.GuildID,
			ChannelID: evt.ChannelID,
			MessageID: msg.ID,
			AuthorID:  msg.Author.ID,
			Body:      msg.Content,
		})
	}
}

func detectMusicChannel(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), DefaultMusicChannelPrefix) {
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

// Message is requeueable if musicbot reacted to it with the requeue command shortcut
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
