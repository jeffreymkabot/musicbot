package music

import (
	"fmt"
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
		svc := b.guildServices[gc.Guild.ID]
		svc.Close()
		b.guildServices[gc.Guild.ID] = Guild(b.session, gc.Guild, b.db)
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

// dispatch request to the corresponding guild service
func onGuildMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	req := GuildRequest{
		Message: message,
		Channel: channel,
		Callback: func(err error) {
			if err != nil {
				b.session.ChannelMessageSend(channel.ID, fmt.Sprintf("🤔...\n%v", err))
			}
		},
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if svc, ok := b.guildServices[channel.GuildID]; ok {
		svc.Send(req)
	}
	return
}

// execute the help command with a nil guild service
func onDirectMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	req := GuildRequest{
		Message: message,
		Channel: channel,
	}
	args := strings.Fields(strings.TrimPrefix(req.Message.Content, defaultCommandPrefix))
	cmd, parsedArgs, ok := parseCommand(args)
	if !ok {
		help.run(nil, req, nil)
	} else if cmd.name != help.name {
		help.run(nil, req, args)
	} else {
		help.run(nil, req, parsedArgs)
	}
}

func onMessageReactionAdd(b *Bot) func(*discordgo.Session, *discordgo.MessageReactionAdd) {
	return func(session *discordgo.Session, react *discordgo.MessageReactionAdd) {
		// log.Printf("message reaction add %#v", react.MessageReaction)
		onReaction(b, session, react.MessageReaction)
	}
}

func onMessageReactionRemove(b *Bot) func(*discordgo.Session, *discordgo.MessageReactionRemove) {
	return func(session *discordgo.Session, react *discordgo.MessageReactionRemove) {
		// log.Printf("message reaction remove %#v", react.MessageReaction)
		onReaction(b, session, react.MessageReaction)
	}
}

// TODO port reaction -> GuildRequest
func onReaction(b *Bot, session *discordgo.Session, react *discordgo.MessageReaction) {
	author, err := session.User(react.UserID)
	if err != nil || author.Bot {
		return
	}

	channel, err := session.State.Channel(react.ChannelID)
	if err != nil {
		return
	}

	message, err := session.State.Message(react.ChannelID, react.MessageID)
	if err != nil {
		return
	}

	req := GuildRequest{
		Channel: channel,
		Message: message,
	}
	// TODO
	_ = req
}
