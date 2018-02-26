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
		log.Printf("guild create %v", gc.Guild.ID)
		// cleanup existing guild service if exists
		// e.g. unhandled disconnect, kick and reinvite
		b.mu.Lock()
		defer b.mu.Unlock()
		svc, ok := b.guildServices[gc.Guild.ID]
		if ok {
			svc.Close()
		}
		b.guildServices[gc.Guild.ID] = Guild(
			gc.Guild,
			session,
			b.db,
			b.commands,
			b.plugins,
		)
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
	defer b.mu.RUnlock()
	if svc, ok := b.guildServices[channel.GuildID]; ok {
		svc.Send(evt)
	}
}

// execute the help command with a nil guild service
func onDirectMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	evt := GuildEvent{
		Channel: *channel,
		Message: *message,
	}
	args := strings.Fields(strings.TrimPrefix(evt.Message.Content, defaultCommandPrefix))
	cmd, parsedArgs, ok := matchCommand(b.commands, args)
	if !ok {
		help.run(nil, evt, nil)
	} else if cmd.name != help.name {
		help.run(nil, evt, args)
	} else {
		help.run(nil, evt, parsedArgs)
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
	defer b.mu.RUnlock()
	if svc, ok := b.guildServices[channel.GuildID]; ok {
		svc.Send(evt)
	}
}
