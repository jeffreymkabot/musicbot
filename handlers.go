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

func onReady(b *Bot) func(s *discordgo.Session, r *discordgo.Ready) {
	return func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("ready %#v\n", r)
		for _, g := range r.Guilds {
			if !g.Unavailable {
				gc := &discordgo.GuildCreate{Guild: g}
				onGuildCreate(b)(s, gc)
			}
		}
		s.AddHandler(onGuildCreate(b))
		s.AddHandler(onMessageCreate(b))
		s.UpdateStatus(0, fmt.Sprintf("%s yt; %s sc; %s skip; %s pause", defaultPrefix, defaultPrefix, defaultPrefix, defaultPrefix))
	}
}

func onGuildCreate(b *Bot) func(s *discordgo.Session, g *discordgo.GuildCreate) {
	return func(s *discordgo.Session, g *discordgo.GuildCreate) {
		log.Printf("guild create %#v\n", g.Guild)

		// cleanup existing guild structure if exists
		// e.g. disconnected, kicked and reinvited
		b.mu.RLock()
		gu, ok := b.guilds[g.ID]
		b.mu.RUnlock()
		if ok && gu.play != nil {
			gu.close()
		}

		gInfo := guildInfo{}
		err := b.db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val := bucket.Get([]byte(g.ID))
			if val == nil {
				return nil
			}
			return json.Unmarshal(val, &gInfo)
		})
		if err != nil {
			log.Printf("error lookup guild in db %v", err)
		}
		log.Printf("found existing guildinfo %#v", gInfo)

		musicChannelID := guildMusicChannelID(s, g.ID)
		log.Printf("music channel in %v: %v", g.ID, musicChannelID)
		if musicChannelID == "" {
			musicChannelID = g.AfkChannelID
		}
		player := dgv.Connect(s, g.ID, musicChannelID, dgv.QueueLength(10))
		b.mu.Lock()
		b.guilds[g.ID] = &guild{
			guildID:   g.ID,
			play:      player,
			guildInfo: gInfo,
		}
		b.mu.Unlock()
	}
}

func onMessageCreate(b *Bot) func(s *discordgo.Session, m *discordgo.MessageCreate) {
	return func(s *discordgo.Session, m *discordgo.MessageCreate) {
		log.Printf("message %#v\n", m.Message)
		textChannel, err := s.State.Channel(m.ChannelID)
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

		if !strings.HasPrefix(m.Content, prefix) {
			return
		}

		args := strings.Fields(strings.TrimPrefix(m.Content, prefix))
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

		log.Printf("exec command %v in %v with %v\n", cmd.name, guild.guildID, args)
		b.exec(cmd, guild, m.Author.ID, m.ID, textChannel.ID, args)
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
