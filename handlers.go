package music

import (
	"encoding/json"
	"fmt"
	"log"
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
		s.UpdateStatus(0, fmt.Sprintf("%s youtube; %s skip", defaultPrefix, defaultPrefix))
	}
}

func onGuildCreate(b *Bot) func(s *discordgo.Session, g *discordgo.GuildCreate) {
	return func(s *discordgo.Session, g *discordgo.GuildCreate) {
		log.Printf("guild create %#v\n", g.Guild)
		b.mu.RLock()
		gu, ok := b.guilds[g.ID]
		b.mu.RUnlock()
		if ok {
			gu.quit()
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
		send, quit := dgv.Connect(s, g.ID, musicChannelID, dgv.QueueLength(10))
		b.mu.Lock()
		b.guilds[g.ID] = &guild{
			guildID:   g.ID,
			send:      send,
			quit:      quit,
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

		for _, cmd := range b.commands {
			if strings.ToLower(args[0]) == cmd.name {
				err := b.exec(cmd, guild, m.Author.ID, textChannel.ID, args[1:])
				if err != nil {
					s.ChannelMessageSend(textChannel.ID, err.Error())
				}
				return
			}
		}
	}
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
