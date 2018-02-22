package music

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
)

const defaultCommandPrefix = "#!"
const defaultMusicChannelPrefix = "music"

// Soundcloud sets the clientID required by the soundcloud API and enables use of the soundcloud command.
func Soundcloud(clientID string) {
	// TODO
	// b.soundcloud = clientID
}

type Bot struct {
	mu            sync.RWMutex
	session       *discordgo.Session
	db            *boltGuildStorage
	owner         string
	soundcloud    string
	me            *discordgo.User
	guildServices map[string]GuildService
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
		session:       session,
		db:            db,
		owner:         owner,
		guildServices: make(map[string]GuildService),
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
	for _, gh := range b.guildServices {
		gh.Close()
	}
	b.session.Close()
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
			return nil
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
