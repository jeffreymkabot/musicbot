package music

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"text/tabwriter"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

var ErrGuildCmd = errors.New("call this command in a guild")

type command struct {
	name            string
	alias           []string
	usage           string // should at least have usage
	short           string
	long            string
	ownerOnly       bool
	restrictChannel bool
	ack             string // must be an emoji, used to react on success
	run             func(*guildService, guildRequest, []string) error
}

func matchCommand(args []string) (*command, []string) {
	// TODO
	return nil, nil
}

func helpWithCommand(cmd *command) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}
	embed.Title = cmd.name
	embed.Fields = []*discordgo.MessageEmbedField{
		&discordgo.MessageEmbedField{
			Name:  "Usage",
			Value: fmt.Sprintf("`%s`", cmd.usage),
		},
	}
	if cmd.long != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Description",
			Value: cmd.long,
		})
	}
	if len(cmd.alias) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Aliases",
			Value: fmt.Sprintf("`%s`", strings.Join(cmd.alias, "`, `")),
		})
	}
	if cmd.restrictChannel {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: "This command will only run in whitelisted channels (see whitelist).",
		}
	}
	return embed
}

// TODO help references commands slice -> initialization loop
var commands []*command

// var commands = []*command{
// 	&help,
// 	&youtube,
// 	&soundcloud,
// 	&twitch,
// 	&bandcamp,
// 	&skip,
// 	&pause,
// 	&clear,
// 	&reconnect,
// 	// setPrefix,
// 	&setListen,
// 	&unsetListen,
// }

func helpWithCommandList(commands []*command) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}
	embed.Title = "help"
	embed.Description = "Command can be inferred from a url if the url's domain is a command name or alias."
	buf := &bytes.Buffer{}
	w := tabwriter.NewWriter(buf, 4, 4, 0, '.', 0)
	for _, cmd := range commands {
		if !cmd.ownerOnly {
			aliasList := ""
			if len(cmd.alias) > 0 {
				aliasList = "`" + strings.Join(cmd.alias, "`, `") + "`"
			}
			restrictChannel := ""
			if cmd.restrictChannel {
				restrictChannel = "*"
			}
			fmt.Fprintf(w, "`%s%s..\t` %s\n", restrictChannel, cmd.name, aliasList)
		}
	}
	w.Flush()
	embed.Fields = []*discordgo.MessageEmbedField{
		&discordgo.MessageEmbedField{
			Name:  "Commands",
			Value: buf.String(),
		},
	}
	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: "Commands with a * will only run in whitelisted channels.",
	}
	return embed
}

var help = command{
	name:  "help",
	alias: []string{"h"},
	usage: "help [command name]",
	ack:   "📬",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		// help gets whispered to the user
		var dm *discordgo.Channel
		var err error
		if req.channel.Type == discordgo.ChannelTypeDM || req.channel.Type == discordgo.ChannelTypeGroupDM {
			dm = req.channel
		} else if dm, err = gsvc.session.UserChannelCreate(req.message.Author.ID); err != nil {
			return err
		}

		if len(args) > 0 && args[0] != "" {
			if cmd := commandByNameOrAlias(commands, args[0]); cmd != nil {
				embed := helpWithCommand(cmd)
				_, err = gsvc.session.ChannelMessageSendEmbed(dm.ID, embed)
				return err
			}
		}

		embed := helpWithCommandList(commands)
		_, err = gsvc.session.ChannelMessageSendEmbed(dm.ID, embed)
		return err
	},
}

var reconnect = command{
	name:            "reconnect",
	usage:           "reconnect",
	restrictChannel: true,
	ack:             "🆗",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		gsvc.reconnect()
		return nil
	},
}

var youtube = command{
	name:            "youtube",
	alias:           []string{"yt", "youtu"},
	usage:           "youtube [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("video please")
		}
		return gsvc.enqueue(plugins.Youtube{}, args[0], req.channel.ID)
	},
}

var soundcloud = command{
	name:            "soundcloud",
	alias:           []string{"sc", "snd"},
	usage:           "soundcloud [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("track please")
		}
		// TODO soundcloud client id
		return gsvc.enqueue(plugins.Soundcloud{ClientID: ""}, args[0], req.channel.ID)
	},
}

var bandcamp = command{
	name:            "bandcamp",
	alias:           []string{"bc"},
	usage:           "bandcamp [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("track please")
		}
		return gsvc.enqueue(plugins.Bandcamp{}, args[0], req.channel.ID)
	},
}

var twitch = command{
	name:            "twitch",
	usage:           "twitch [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("channel please")
		}
		return gsvc.enqueue(plugins.Twitch{}, args[0], req.channel.ID)
	},
}

var skip = command{
	name:            "skip",
	usage:           "skip",
	restrictChannel: true,
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if err := gsvc.player.Skip(); err != nil {
			log.Print("nop skip")
		}
		return nil
	},
}

var pause = command{
	name:            "pause",
	alias:           []string{"p"},
	usage:           "pause",
	restrictChannel: true,
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if err := gsvc.player.Pause(); err != nil {
			log.Print("nop pause")
		}
		return nil
	},
}

var clear = command{
	name:            "clear",
	alias:           []string{"cl"},
	usage:           "clear",
	restrictChannel: true,
	ack:             "🔘",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		return gsvc.player.Clear()
	},
}

var setPrefix = command{
	name:  "prefix",
	usage: "prefix",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		if gsvc == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 || args[0] == "" {
			return errors.New("prefix please")
		}
		// gsvc.mu.Lock()
		gsvc.Prefix = args[0]
		// gsvc.mu.Unlock()
		// db
		return nil
	},
}

var setListen = command{
	name:  "whitelist",
	usage: "whitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		textChannelID := req.channel.ID
		if gsvc == nil {
			return ErrGuildCmd
		}
		if textChannelID == "" {
			return errors.New("channel please")
		}
		// gsvc.mu.Lock()
		if !contains(gsvc.ListenChannels, textChannelID) {
			gsvc.ListenChannels = append(gsvc.ListenChannels, textChannelID)
		}
		// gsvc.mu.Unlock()
		// db
		return gsvc.save()
		// return b.db.Update(func(tx *bolt.Tx) error {
		// 	bucket := tx.Bucket([]byte("guilds"))
		// 	val, err := json.Marshal(gsvc.guildInfo)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	return bucket.Put([]byte(gsvc.guildID), val)
		// })
	},
}

var unsetListen = command{
	name:  "unwhitelist",
	usage: "unwhitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, req guildRequest, args []string) error {
		textChannelID := req.channel.ID
		if gsvc == nil {
			return ErrGuildCmd
		}
		if textChannelID == "" {
			return errors.New("channel please")
		}
		// gsvc.mu.Lock()
		for i, ch := range gsvc.ListenChannels {
			if ch == textChannelID {
				gsvc.ListenChannels = append(gsvc.ListenChannels[:i], gsvc.ListenChannels[i+1:]...)
			}
		}
		// gsvc.mu.Unlock()
		// db
		return gsvc.save()
		// return b.db.Update(func(tx *bolt.Tx) error {
		// 	bucket := tx.Bucket([]byte("guilds"))
		// 	val, err := json.Marshal(gsvc.guildInfo)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	return bucket.Put([]byte(gsvc.guildID), val)
		// })
	},
}
