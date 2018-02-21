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
		session.UpdateStatus(0, fmt.Sprintf("%s help", defaultCommandPrefix))
	}
}

func onGuildCreate(b *Bot) func(*discordgo.Session, *discordgo.GuildCreate) {
	return func(session *discordgo.Session, gc *discordgo.GuildCreate) {
		log.Printf("guild create %v", gc.Guild.ID)

		// will cleanup existing guild structure if exists
		// e.g. disconnected, kicked and reinvited
		b.addGuild(gc.Guild)
	}
}

// worth noting that discordgo event handlers are executed in new goroutines,
// hence all command invocations are necessarily async
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

func onDirectMessage(b *Bot, msg *discordgo.Message, ch *discordgo.Channel) {
	args := strings.Fields(strings.TrimPrefix(msg.Content, defaultCommandPrefix))
	if len(args) > 0 {
		if commandByNameOrAlias(strings.ToLower(args[0])) == &help {
			args = args[1:]
		}
	}

	req := guildRequest{
		message: msg,
		channel: ch,
	}
	help.run(nil, req, args)
}

func onGuildMessage(b *Bot, message *discordgo.Message, channel *discordgo.Channel) {
	req := guildRequest{
		guildID: channel.GuildID,
		message: message,
		channel: channel,
		callback: func(err error) {
			if err != nil {
				b.session.ChannelMessageSend(channel.ID, fmt.Sprintf("🤔...\n%v", err))
			}
		},
	}
	if ch := b.guildHandlers[channel.GuildID]; ch != nil {
		ch <- req
	}
	return
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

const (
	pauseCmdEmoji = "⏯"
	skipCmdEmoji  = "⏭"
)

func onReaction(b *Bot, session *discordgo.Session, react *discordgo.MessageReaction) {
	author, err := session.User(react.UserID)
	if err != nil || author.Bot {
		return
	}

	ch, err := session.State.Channel(react.ChannelID)
	if err != nil {
		return
	}

	// if ch := b.guildHandlers[channel.GuildID]; ch != nil {
	// 	ch <- req
	// }

	b.mu.RLock()
	gsvc, ok := b.guilds[ch.GuildID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	statusMsgID, statusMsgChID := "", ""
	// gsvc.mu.RLock()
	if gsvc.statusMsg != nil {
		statusMsgID, statusMsgChID = gsvc.statusMsg.ID, gsvc.statusMsg.ChannelID
	}
	// gsvc.mu.RUnlock()
	// TODO how to handle reaction commands
	if react.MessageID == statusMsgID && react.ChannelID == statusMsgChID {
		switch react.Emoji.Name {
		case pauseCmdEmoji:
			// pause.run(nil, gsvc, nil)
		case skipCmdEmoji:
			// skip.run(nil, gsvc, nil)
		}
	}
}
