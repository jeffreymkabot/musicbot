package musicbot

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jeffreymkabot/musicbot/status"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const DefaultCommandPrefix = "#!"

// DefaultGuildConfig is the starting configuration for a guild.
var DefaultGuildConfig = GuildConfig{
	Prefix: "#!",
}

// ErrGuildServiceTimeout indicates that a guild service has taken too long to accept an event.
var ErrGuildServiceTimeout = errors.New("service timed out")

// ErrGuildServiceClosed indicates that a guild service has been closed.
var ErrGuildServiceClosed = errors.New("service is disposed")

// GuildEvent provides instructions to a Guild.
type GuildEvent interface {
	eventType()
}

type MessageEvent struct {
	Message *discordgo.Message
}

func (MessageEvent) eventType() {}

type ReactEvent struct {
	Reaction *discordgo.MessageReaction
}

func (ReactEvent) eventType() {}

func (r ReactEvent) pseudoMessageEvent() MessageEvent {
	return MessageEvent{
		Message: &discordgo.Message{
			GuildID:   r.Reaction.GuildID,
			ChannelID: r.Reaction.ChannelID,
			Content:   r.Reaction.Emoji.Name,
		},
	}
}

// func (evt GuildEvent) String() string {
// 	return fmt.Sprintf("Guild:%v|Channel:%v|Author:%v|Body:%v",
// 		evt.GuildID, evt.ChannelID, evt.AuthorID, evt.Body)
// }

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
	buttons      []status.Button
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
	openPlayer func(idleChannelID string, buttons []status.Button) GuildPlayer,
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
		commands:     commands,
		plugins:      plugins,
	}

	buttons := []status.Button{
		{
			Emoji: pause.shortcut,
			Action: func(_ string) {
				gsvc.player.Pause()
			},
		},
		{
			Emoji: skip.shortcut,
			Action: func(_ string) {
				gsvc.player.Skip()
			},
		},
		{
			Emoji: requeue.shortcut,
			Action: func(_ string) {
				// TODO
			},
		},
		{
			Emoji: help.shortcut,
			Action: func(userID string) {
				// TODO
			},
		},
	}

	gsvc.buttons = buttons
	gsvc.player = openPlayer(info.MusicChannel, buttons)

	go func(events <-chan GuildEvent) {
		for evt := range eventChan {
			switch evt := evt.(type) {
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
// HandleMessageEvent acts only on messages that begin with the configured prefix or mention musicbot
// (and if applicable, are in a configured text channel).
func (gsvc *GuildService) HandleMessageEvent(evt MessageEvent) {
	arg, ok := gsvc.checkMessageEvent(evt)
	if !ok {
		return
	}

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

// check that the message begins with an appropriate prefix or begins with mentioning musicbot
func (gsvc *GuildService) checkMessageEvent(evt MessageEvent) (string, bool) {
	me := gsvc.discord.State.User
	message := evt.Message
	if gsvc.Prefix != "" && strings.HasPrefix(message.Content, gsvc.Prefix) {
		arg := strings.TrimSpace(strings.TrimPrefix(message.Content, gsvc.Prefix))
		return arg, true
	}

	if strings.HasPrefix(message.Content, DefaultCommandPrefix) {
		arg := strings.TrimSpace(strings.TrimPrefix(message.Content, DefaultCommandPrefix))
		return arg, true
	}

	for _, mention := range message.Mentions {
		if mention.ID == me.ID && strings.HasPrefix(message.Content, me.Mention()) {
			arg := strings.TrimSpace(strings.TrimPrefix(message.Content, me.Mention()))
			return arg, true
		}
	}

	return "", false
}

func (gsvc *GuildService) isAllowed(cmd command, evt MessageEvent) bool {
	channelOK := !cmd.restrictChannel || contains(gsvc.ListenChannels, evt.Message.ChannelID)
	authorOK := !cmd.ownerOnly || evt.Message.Author.ID == gsvc.guildOwnerID
	return channelOK && authorOK
}

func (gsvc *GuildService) runAndRespondToMessage(fn serviceFunc, evt MessageEvent, args []string, ack string) {
	err := fn(gsvc, evt, args)
	// error response
	if err != nil {
		gsvc.discord.ChannelMessageSend(evt.Message.ChannelID, fmt.Sprintf("🤔...\n%v", err))
		return
	}
	// success ack
	if ack != "" {
		gsvc.discord.MessageReactionAdd(evt.Message.ChannelID, evt.Message.ID, ack)
	}
}

// HandleReactEvent may invoke a command corresponding to the reacted emoji
// musicbot puts its own reactions in these locations so users do not have to guess what emojis do what.
func (gsvc *GuildService) HandleReactEvent(evt ReactEvent) {
	emoji := evt.Reaction.Emoji.Name
	if requeue.shortcut != emoji {
		return
	}

	// react event does not have full message struct, try to recover the message
	msg, err := gsvc.discord.State.Message(evt.Reaction.ChannelID, evt.Reaction.MessageID)
	if err != nil {
		msg, err = gsvc.discord.ChannelMessage(evt.Reaction.ChannelID, evt.Reaction.MessageID)
		if err != nil {
			return
		}
	}

	if requeueable(msg) {
		// act as though the reacted message event happened again
		gsvc.HandleMessageEvent(MessageEvent{
			Message: msg,
		})
	}
}

var detectMusicChannelPattern = regexp.MustCompile(`(?i)\bmusic\b`)

func detectMusicChannel(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && detectMusicChannelPattern.MatchString(ch.Name) {
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

// Message is requeueable if it was not sent by a bot and
// musicbot reacted to it with the requeue command shortcut
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
