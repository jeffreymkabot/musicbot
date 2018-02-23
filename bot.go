package music

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
)

const defaultCommandPrefix = "#!"
const defaultMusicChannelPrefix = "music"

type Bot struct {
	mu            sync.RWMutex
	discord       *discordgo.Session
	db            *boltGuildStorage
	owner         string
	me            *discordgo.User
	guildServices map[string]GuildService
}

func New(token string, dbPath string, owner string) (*Bot, error) {
	db, err := newBoltGuildStorage(dbPath)
	if err != nil {
		return nil, err
	}

	discord, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	discord.LogLevel = discordgo.LogWarning
	b := &Bot{
		discord:       discord,
		db:            db,
		owner:         owner,
		guildServices: make(map[string]GuildService),
	}

	discord.AddHandler(onGuildCreate(b))
	discord.AddHandler(onMessageCreate(b))
	discord.AddHandler(onReady(b))
	discord.AddHandler(onMessageReactionAdd(b))
	discord.AddHandler(onMessageReactionRemove(b))

	err = discord.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	// possible to take this from ready instead???
	b.me, err = discord.User("@me")
	if err != nil {
		discord.Close()
		db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	for _, svc := range b.guildServices {
		svc.Close()
	}
	b.discord.Close()
	b.db.Close()
	b.mu.Unlock()
}

func detectMusicChannel(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), defaultMusicChannelPrefix) {
			return ch.ID
		}
	}
	return ""
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

func (db boltGuildStorage) Read(guildID string, info *GuildInfo) error {
	return db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val := bucket.Get([]byte(guildID))
		if val == nil {
			return errors.New("guild not found")
		}
		return json.Unmarshal(val, info)
	})
}

func (db boltGuildStorage) Write(guildID string, info GuildInfo) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val, err := json.Marshal(info)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(guildID), val)
	})
}
