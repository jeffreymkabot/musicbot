package music

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dgv "github.com/jeffreymkabot/discordvoice"
)

const musicChannelPrefix = "music"

func onReady(b *Bot) func(*discordgo.Session, *discordgo.Ready) {
	return func(session *discordgo.Session, ready *discordgo.Ready) {
		log.Printf("ready %#v\n", ready)
		for _, g := range ready.Guilds {
			if !g.Unavailable {
				gc := &discordgo.GuildCreate{Guild: g}
				onGuildCreate(b)(session, gc)
			}
		}
		session.UpdateStatus(0, fmt.Sprintf("%s yt; %s sc; %s skip; %s pause", defaultPrefix, defaultPrefix, defaultPrefix, defaultPrefix))
	}
}

func onGuildCreate(b *Bot) func(*discordgo.Session, *discordgo.GuildCreate) {
	return func(session *discordgo.Session, gc *discordgo.GuildCreate) {
		log.Printf("guild create %#v\n", gc.Guild)

		// cleanup existing guild structure if exists
		// e.g. disconnected, kicked and reinvited
		b.mu.RLock()
		gu, ok := b.guilds[gc.ID]
		b.mu.RUnlock()
		if ok && gu.play != nil {
			gu.close()
		}

		gInfo := guildInfo{}
		err := b.db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val := bucket.Get([]byte(gc.ID))
			if val == nil {
				return nil
			}
			return json.Unmarshal(val, &gInfo)
		})
		if err != nil {
			log.Printf("error lookup guild in db %v", err)
		}
		log.Printf("found existing guildinfo %#v", gInfo)

		musicChannelID := guildMusicChannelID(session, gc.ID)
		log.Printf("music channel in %v: %v", gc.ID, musicChannelID)
		if musicChannelID == "" {
			musicChannelID = gc.AfkChannelID
		}
		player := dgv.Connect(session, gc.ID, musicChannelID, dgv.QueueLength(10))
		b.mu.Lock()
		b.guilds[gc.ID] = &guild{
			guildID:   gc.ID,
			play:      player,
			guildInfo: gInfo,
		}
		b.mu.Unlock()
	}
}

func onMessageCreate(b *Bot) func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(session *discordgo.Session, mc *discordgo.MessageCreate) {
		log.Printf("message %#v\n", mc.Message)
		textChannel, err := session.State.Channel(mc.ChannelID)
		if err != nil {
			return
		}
		if textChannel.GuildID == "" {
			return
		}

		b.mu.RLock()
		guild, ok := b.guilds[textChannel.GuildID]
		b.mu.RUnlock()
		if !ok {
			return
		}

		prefix := defaultPrefix
		if guild.Prefix != "" {
			prefix = guild.Prefix
		}

		if !strings.HasPrefix(mc.Content, prefix) {
			return
		}

		args := strings.Fields(strings.TrimPrefix(mc.Content, prefix))
		if len(args) == 0 {
			return
		}

		var candidate string
		// if arg[0] is a valid url, try the url hostname as a command name/alias and the whole url as command's arg[0]
		// if arg[0] is not a url, then arg[0] is the command name/alias and the command's args are arg[1:]
		if strings.HasPrefix(args[0], "http") {
			if u, err := url.Parse(args[0]); err == nil {
				candidate = strings.ToLower(domainFrom(u.Hostname()))
			}
		}
		if candidate == "" {
			candidate = strings.ToLower(args[0])
			args = args[1:]
		}

		log.Printf("candidate cmd %v", candidate)

		cmd := commandByName(b, candidate)
		if cmd == nil {
			return
		}

		b.exec(cmd, guild, mc.Author.ID, mc.ID, textChannel.ID, args)
	}
}

// get "example" in example, example., example.com, www.example.com, www.system.example.com
func domainFrom(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) > 1 {
		return parts[len(parts)-2]
	}
	return parts[0]
}

func commandByName(b *Bot, candidate string) *command {
	for _, c := range b.commands {
		if candidate == c.name {
			return c
		} else if len(c.alias) > 0 {
			for _, a := range c.alias {
				if candidate == a {
					return c
				}
			}
		}
	}
	return nil
}

func guildMusicChannelID(s *discordgo.Session, guildID string) string {
	g, err := s.State.Guild(guildID)
	if err != nil {
		return ""
	}
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), musicChannelPrefix) {
			return ch.ID
		}
	}
	return ""
}
