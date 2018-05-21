package music

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func onReady(b *Bot) func(*discordgo.Session, *discordgo.Ready) {
	return func(session *discordgo.Session, ready *discordgo.Ready) {
		log.Printf("ready %#v", ready)
		for _, g := range ready.Guilds {
			if !g.Unavailable {
				gc := &discordgo.GuildCreate{Guild: g}
				onGuildCreate(b)(session, gc)
			}
		}
		session.UpdateStatus(0, defaultCommandPrefix+" "+help.name)
	}
}

func onGuildCreate(b *Bot) func(*discordgo.Session, *discordgo.GuildCreate) {
	return func(session *discordgo.Session, gc *discordgo.GuildCreate) {
		guildID := gc.Guild.ID
		log.Printf("add guild %v", guildID)

		// alternative is to lookup guild in database here and resolve idlechannel immediately,
		// would have to lookup guild twice or pass info into guild fn
		openPlayer := func(idleChannelID string) GuildPlayer {
			return NewGuildPlayer(
				guildID,
				b.discord,
				idleChannelID,
				commandShortcuts(b.commands),
			)
		}

		b.Register(guildID, Guild(
			gc.Guild,
			b.discord,
			b.db,
			openPlayer,
			b.commands,
			b.plugins,
		))
	}
}

// worth noting that discordgo event handlers are by default executed in new goroutines
// all command invocations are async
func onMessageCreate(b *Bot) func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(session *discordgo.Session, mc *discordgo.MessageCreate) {
		if mc.Author.Bot {
			return
		}

		textChannel, err := session.State.Channel(mc.ChannelID)
		if err != nil {
			return
		}

		switch textChannel.Type {
		case discordgo.ChannelTypeGuildText:
			onGuildMessage(b, mc.Message, textChannel)
		case discordgo.ChannelTypeGroupDM:
			fallthrough
		case discordgo.ChannelTypeDM:
			onDirectMessage(b, mc.Message, textChannel)
		}
	}
}

// dispatch event to the corresponding guild service
func onGuildMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	evt := GuildEvent{
		Type:    MessageEvent,
		Channel: *channel,
		Message: *message,
		Author:  *message.Author,
		Body:    message.Content,
	}

	b.mu.RLock()
	svc, ok := b.guilds[channel.GuildID]
	b.mu.RUnlock()

	if ok {
		svc.Notify(evt)
	}
}

func onDirectMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	evt := GuildEvent{
		Type:    MessageEvent,
		Channel: *channel,
		Message: *message,
		Author:  *message.Author,
		Body:    message.Content,
	}
	arg := strings.TrimPrefix(evt.Message.Content, defaultCommandPrefix)
	cmd, argv, ok := matchCommand(b.commands, arg)
	// help command gets a synthetic guild service, _just_ what is needed to run
	gsvc := &guildService{
		discord:  b.discord,
		commands: b.commands,
	}
	if !ok {
		help.run(gsvc, evt, nil)
	} else if cmd.name == help.name {
		help.run(gsvc, evt, argv)
	} else {
		help.run(gsvc, evt, strings.Fields(arg))
	}
}

func onMessageReactionAdd(b *Bot) func(*discordgo.Session, *discordgo.MessageReactionAdd) {
	return func(session *discordgo.Session, react *discordgo.MessageReactionAdd) {
		onReaction(b, session, react.MessageReaction)
	}
}

func onMessageReactionRemove(b *Bot) func(*discordgo.Session, *discordgo.MessageReactionRemove) {
	return func(session *discordgo.Session, react *discordgo.MessageReactionRemove) {
		onReaction(b, session, react.MessageReaction)
	}
}

// dispatch event to the corresponding guild service
func onReaction(b *Bot, session *discordgo.Session, react *discordgo.MessageReaction) {
	channel, err := session.State.Channel(react.ChannelID)
	if err != nil {
		return
	}

	member, err := session.State.Member(channel.GuildID, react.UserID)
	if err != nil || member.User.Bot {
		return
	}

	evt := GuildEvent{
		Type:    ReactEvent,
		Channel: *channel,
		Message: discordgo.Message{ID: react.MessageID},
		Author:  *member.User,
		Body:    react.Emoji.Name,
	}
	b.mu.RLock()
	svc, ok := b.guilds[channel.GuildID]
	b.mu.RUnlock()

	if ok {
		svc.Notify(evt)
	}
}
