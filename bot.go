package music

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dcv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const defaultCommandPrefix = "#!"
const defaultMusicChannelPrefix = "music"

// Soundcloud sets the clientID required by the soundcloud API and enables use of the soundcloud command.
func Soundcloud(clientID string) {
	// TODO
	// b.soundcloud = clientID
}

type Bot struct {
	mu           sync.RWMutex
	session      *discordgo.Session
	db           *boltGuildStorage
	owner        string
	soundcloud   string
	loudness     float64
	me           *discordgo.User
	guilds       map[string]*guildService
	guildClients map[string]guildClient
}

func New(token string, dbPath string, owner string) (*Bot, error) {
	db, err := newBoltGuildStorage(dbPath)
	if err != nil {
		return nil, err
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	session.LogLevel = discordgo.LogWarning
	b := &Bot{
		session:      session,
		db:           db,
		owner:        owner,
		guilds:       make(map[string]*guildService),
		guildClients: make(map[string]guildClient),
	}

	session.AddHandler(onGuildCreate(b))
	session.AddHandler(onMessageCreate(b))
	session.AddHandler(onReady(b))
	session.AddHandler(onMessageReactionAdd(b))
	session.AddHandler(onMessageReactionRemove(b))

	err = session.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	// possible to take this from ready instead???
	b.me, err = session.User("@me")
	if err != nil {
		session.Close()
		db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	for _, gh := range b.guildClients {
		gh.close()
	}
	b.session.Close()
	b.db.Close()
	b.mu.Unlock()
}

func (b *Bot) enqueue(gsvc *guildService, pl plugins.Plugin, url string, statusChannelID string) error {
	musicChannelID := ""
	// musicChannelID := musicChannelFromGuildID(b.session.State, gsvc.guildID)
	// if musicChannelID == "" {
	// 	return errors.New("no music channel set up")
	// }

	md, err := pl.Resolve(url)
	if err != nil {
		return err
	}

	var msg *discordgo.Message
	embed := &discordgo.MessageEmbed{Color: 0xa680ee}
	embed.Footer = &discordgo.MessageEmbedFooter{}
	updateEmbed := func(isPaused bool, elapsed time.Duration, next string) {
		if isPaused {
			embed.Title = "⏸️ " + md.Title
		} else {
			embed.Title = "▶️ " + md.Title
		}
		embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
		if next != "" {
			embed.Footer.Text = "On Deck: " + next
		}
	}
	updateMessage := func() {
		if msg != nil {
			b.session.ChannelMessageEditEmbed(msg.ChannelID, msg.ID, embed)
			return
		}
		msg, err = b.session.ChannelMessageSendEmbed(statusChannelID, embed)
		if msg != nil {
			// gsvc.mu.Lock()
			gsvc.statusMsg = msg
			// gsvc.mu.Unlock()
			// gsvc.close() will wait until the message is deleted in the OnEnd callback
			// gsvc.wg.Add(1)
			b.session.MessageReactionAdd(statusChannelID, msg.ID, pauseCmdEmoji)
			b.session.MessageReactionAdd(statusChannelID, msg.ID, skipCmdEmoji)
		}
		if err != nil {
			log.Print(err)
		}
	}

	return gsvc.player.Enqueue(
		musicChannelID,
		md.Title,
		md.Open,
		dcv.Duration(md.Duration),
		dcv.Loudness(b.loudness),
		dcv.OnStart(func() {
			updateEmbed(false, 0, gsvc.player.Next())
			updateMessage()
		}),
		dcv.OnPause(func(elapsed time.Duration) {
			updateEmbed(true, elapsed, gsvc.player.Next())
			updateMessage()
		}),
		dcv.OnResume(func(elapsed time.Duration) {
			updateEmbed(false, elapsed, gsvc.player.Next())
			updateMessage()
		}),
		dcv.OnProgress(func(elapsed time.Duration, frameTimes []time.Time) {
			avg, dev, max, min := stats(latencies(frameTimes))
			embed.Fields = []*discordgo.MessageEmbedField{
				&discordgo.MessageEmbedField{
					Name:  "Debug",
					Value: fmt.Sprintf("`avg %.3fms`, `dev %.3fms`, `max %.3fms`, `min %.3fms`", avg, dev, max, min),
				},
			}
			updateEmbed(false, elapsed, gsvc.player.Next())
			updateMessage()
		}, 10*time.Second),
		dcv.OnEnd(func(elapsed time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", elapsed, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			if msg != nil {
				b.session.ChannelMessageDelete(msg.ChannelID, msg.ID)
				// gsvc.mu.Lock()
				gsvc.statusMsg = nil
				// gsvc.mu.Unlock()
				// gsvc.wg.Done()
			}
		}),
	)
}

func latencies(times []time.Time) []float64 {
	// log.Print(times)
	latencies := make([]float64, len(times)-1)
	for i := 1; i < len(times); i++ {
		latencies[i-1] = float64(times[i].Sub(times[i-1]).Nanoseconds()) / 1e6
	}
	// log.Print(latencies)
	return latencies
}

func stats(data []float64) (avg float64, dev float64, max float64, min float64) {
	if len(data) == 0 {
		return
	}
	min = math.MaxFloat64
	sum := 0.0
	for _, v := range data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg = sum / float64(len(data))
	for _, v := range data {
		dev += ((v - avg) * (v - avg))
	}
	dev = dev / float64(len(data))
	dev = math.Sqrt(dev)
	return
}

func (b *Bot) addGuild(g *discordgo.Guild) {
	b.mu.Lock()
	defer b.mu.Unlock()
	gh := b.guildClients[g.ID]
	gh.close()
	b.guildClients[g.ID] = newGuild(b.session, g, b.db)
}

func detectMusicChannel(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), defaultMusicChannelPrefix) {
			return ch.ID
		}
	}
	return ""
}

func prettyTime(t time.Duration) string {
	hours := int(t.Hours())
	min := int(t.Minutes()) % 60
	sec := int(t.Seconds()) % 60
	if hours >= 1 {
		return fmt.Sprintf("%02v:%02v:%02v", hours, min, sec)
	}
	return fmt.Sprintf("%02v:%02v", min, sec)
}

type boltGuildStorage struct {
	*bolt.DB
}

func newBoltGuildStorage(dbPath string) (*boltGuildStorage, error) {
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, err
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("guilds"))
		return err
	}); err != nil {
		return nil, err
	}
	return &boltGuildStorage{db}, nil
}

func (db boltGuildStorage) read(guildID string, info *guildInfo) error {
	return db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val := bucket.Get([]byte(guildID))
		if val == nil {
			return nil
		}
		return json.Unmarshal(val, info)
	})
}

func (db boltGuildStorage) write(guildID string, info guildInfo) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val, err := json.Marshal(info)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(guildID), val)
	})
}
