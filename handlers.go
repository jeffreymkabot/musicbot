package musicbot

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func onReady(readyChan chan<- *discordgo.Ready) func(*discordgo.Session, *discordgo.Ready) {
	return func(session *discordgo.Session, ready *discordgo.Ready) {
		readyChan <- ready
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

		b.Register(guildID, NewGuild(
			gc.Guild,
			b.discord,
			b.db,
			openPlayer,
			b.commands,
			b.plugins,
		))
	}
}

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
		Type:      MessageEvent,
		GuildID:   channel.GuildID,
		ChannelID: channel.ID,
		MessageID: message.ID,
		AuthorID:  message.Author.ID,
		Body:      message.Content,
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
		Type:      MessageEvent,
		GuildID:   channel.GuildID,
		ChannelID: channel.ID,
		MessageID: message.ID,
		AuthorID:  message.Author.ID,
		Body:      message.Content,
	}
	arg := strings.TrimPrefix(evt.Body, DefaultCommandPrefix)
	cmd, argv, ok := matchCommand(b.commands, arg)
	// help command gets a synthetic guild service, _just_ what is needed to run
	gsvc := &GuildService{
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
		Type:      ReactEvent,
		GuildID:   channel.GuildID,
		ChannelID: channel.ID,
		MessageID: react.MessageID,
		AuthorID:  react.UserID,
		Body:      react.Emoji.Name,
	}
	b.mu.RLock()
	svc, ok := b.guilds[channel.GuildID]
	b.mu.RUnlock()

	if ok {
		svc.Notify(evt)
	}
}
