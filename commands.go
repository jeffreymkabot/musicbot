package music

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"reflect"
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
	run             func(*guildService, GuildRequest, []string) error
}

// TODO help references commands slice -> initialization loop
// review use of global slice :/
var commands []*command

// nil if none matching
func parseCommand(args []string) (*command, []string) {
	if len(args) == 0 {
		return nil, args
	}
	// if arg[0] resembles a url try the domain as a command name/alias and args are arg[0:]
	// else try arg[0] is a command name/alias and args are arg[1:]
	candidateCmd := ""
	if strings.HasPrefix(args[0], "http") {
		if u, err := url.Parse(args[0]); err == nil {
			candidateCmd = strings.ToLower(domainFrom(u.Hostname()))
			if cmd := commandByNameOrAlias(candidateCmd); cmd != nil {
				return cmd, args
			}
		}
	}
	candidateCmd = strings.ToLower(args[0])
	if cmd := commandByNameOrAlias(candidateCmd); cmd != nil {
		return cmd, args[1:]
	}
	// TODO if still none try to see if streamlink will handle the url and synthesize a command
	return nil, args
}

// get "example" in example, example., example.com, www.example.com, www.system.example.com
func domainFrom(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) < 3 {
		return parts[0]
	}
	return parts[len(parts)-2]
}

func commandByNameOrAlias(candidate string) *command {
	for _, cmd := range commands {
		if candidate == cmd.name {
			return cmd
		}
		for _, alias := range cmd.alias {
			if candidate == alias {
				return cmd
			}
		}
	}
	return nil
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
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		// help gets whispered to the user
		var dm *discordgo.Channel
		var err error
		if req.Channel.Type == discordgo.ChannelTypeDM || req.Channel.Type == discordgo.ChannelTypeGroupDM {
			dm = req.Channel
		} else if dm, err = gsvc.session.UserChannelCreate(req.Message.Author.ID); err != nil {
			return err
		}

		if len(args) > 0 && args[0] != "" {
			if cmd := commandByNameOrAlias(args[0]); cmd != nil {
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
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
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
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}
		return gsvc.enqueue(plugins.Youtube{}, args[0], req.Channel.ID)
	},
}

var soundcloud = command{
	name:            "soundcloud",
	alias:           []string{"sc", "snd"},
	usage:           "soundcloud [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}
		// TODO soundcloud client id
		return gsvc.enqueue(plugins.Soundcloud{ClientID: ""}, args[0], req.Channel.ID)
	},
}

var bandcamp = command{
	name:            "bandcamp",
	alias:           []string{"bc"},
	usage:           "bandcamp [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}
		return gsvc.enqueue(plugins.Bandcamp{}, args[0], req.Channel.ID)
	},
}

var twitch = command{
	name:            "twitch",
	usage:           "twitch [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) == 0 {
			return errors.New("channel please")
		}
		return gsvc.enqueue(plugins.Twitch{}, args[0], req.Channel.ID)
	},
}

var skip = command{
	name:            "skip",
	usage:           "skip",
	restrictChannel: true,
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		gsvc.player.Skip()
		return nil
	},
}

var pause = command{
	name:            "pause",
	alias:           []string{"p"},
	usage:           "pause",
	restrictChannel: true,
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		gsvc.player.Pause()
		return nil
	},
}

var clear = command{
	name:            "clear",
	alias:           []string{"cl"},
	usage:           "clear",
	restrictChannel: true,
	ack:             "🔘",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		gsvc.player.Clear()
		return nil
	},
}

var get = command{
	name:  "get",
	usage: "get [field]",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) == 0 {
			return errors.New("field please")
		}
		info := reflect.ValueOf(&gsvc.GuildInfo).Elem()
		infoType := info.Type()
		for i := 0; i < infoType.NumField(); i++ {
			fldName := infoType.Field(i).Name
			if strings.ToLower(fldName) == strings.ToLower(args[0]) {
				val := info.Field(i).Interface()
				gsvc.session.ChannelMessageSend(req.Channel.ID, fmt.Sprintf("`%v: %v`", fldName, val))
				return nil
			}
		}
		return errors.New("field not found")
	},
}

var set = command{
	name:  "set",
	usage: "set [field] [value]",
	ack:   "🆗",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		if len(args) < 2 {
			return errors.New("field and value please")
		}
		info := reflect.ValueOf(&gsvc.GuildInfo).Elem()
		infoType := info.Type()
		for i := 0; i < infoType.NumField(); i++ {
			fldName := infoType.Field(i).Name
			fldType := infoType.Field(i).Type.Kind()
			// TODO support non string fields e.g. loudness is a float64
			if fldType == reflect.String && strings.ToLower(fldName) == strings.ToLower(args[0]) {
				info.Field(i).SetString(args[1])
				gsvc.save()
				return nil
			}
		}
		return errors.New("settable field not found")
	},
}

var setListen = command{
	name:  "whitelist",
	usage: "whitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		textChannelID := req.Channel.ID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		if !contains(gsvc.ListenChannels, textChannelID) {
			gsvc.ListenChannels = append(gsvc.ListenChannels, textChannelID)
		}
		return gsvc.save()
	},
}

var unsetListen = command{
	name:  "unwhitelist",
	usage: "unwhitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, req GuildRequest, args []string) error {
		textChannelID := req.Channel.ID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		for i, ch := range gsvc.ListenChannels {
			if ch == textChannelID {
				gsvc.ListenChannels = append(gsvc.ListenChannels[:i], gsvc.ListenChannels[i+1:]...)
			}
		}
		return gsvc.save()
	},
}
