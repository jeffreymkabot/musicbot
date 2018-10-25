package musicbot

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

// Bot provides resources to and routes events to the appropriate guild service.
type Bot struct {
	quit     chan struct{}
	me       *discordgo.User
	discord  *discordgo.Session
	db       *boltGuildStorage
	commands []command
	plugins  []plugins.Plugin

	mu     sync.RWMutex
	guilds map[string]*Guild
}

// New starts a musicbot server.
func New(token string, dbPath string, soundcloud string, youtube string) (*Bot, error) {
	db, err := newBoltGuildStorage(dbPath)
	if err != nil {
		return nil, err
	}

	discord, err := discordgo.New("Bot " + token)
	if err != nil {
		db.Close()
		return nil, err
	}
	discord.LogLevel = discordgo.LogInformational

	b := &Bot{
		quit:    make(chan struct{}),
		discord: discord,
		db:      db,
		commands: []command{
			help,
			playlist,
			pause,
			skip,
			clear,
			requeue,
			reconnect,
			get,
			set,
			setPlayback,
			setListen,
			unsetListen,
		},
		plugins: []plugins.Plugin{
			plugins.Youtube{},
			plugins.Soundcloud{ClientID: soundcloud},
			plugins.Twitch{},
			plugins.Bandcamp{},
			plugins.Streamlink{},
		},
		guilds: make(map[string]*Guild),
	}
	youtubeSearch, err := plugins.NewYoutubeSearch(youtube)
	if err == nil {
		b.plugins = append(b.plugins, youtubeSearch)
	} else {
		log.Printf("failed to acquire youtube service %v", err)
	}

	discord.AddHandler(onGuildCreate(b))
	discord.AddHandler(onMessageCreate(b))
	discord.AddHandler(onMessageReactionAdd(b))
	discord.AddHandler(onMessageReactionRemove(b))

	// TODO review behavior around secondary ready events
	readyChan := make(chan *discordgo.Ready)
	discord.AddHandlerOnce(onReady(readyChan))

	err = discord.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	var ready *discordgo.Ready
	select {
	case ready = <-readyChan:
	case <-time.After(10 * time.Second):
		discord.Close()
		db.Close()
		return nil, errors.New("failed to receive READY event in time")
	}
	log.Printf("got READY event with %v guilds", len(ready.Guilds))
	b.me = ready.User

	for _, g := range ready.Guilds {
		if !g.Unavailable {
			gc := &discordgo.GuildCreate{Guild: g}
			onGuildCreate(b)(discord, gc)
		}
	}

	go statusLoop(discord, b.quit)

	return b, nil
}

func statusLoop(discord *discordgo.Session, quit <-chan struct{}) {
	tick := time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-tick.C:
			discord.UpdateStatus(0, DefaultCommandPrefix+" "+help.name)
		case <-quit:
			tick.Stop()
			return
		}
	}
}

// Stop closes all services, resources, and goroutines.
func (b *Bot) Stop() {
	select {
	case <-b.quit:
	default:
		close(b.quit)
	}

	b.mu.Lock()
	for _, svc := range b.guilds {
		svc.Close()
	}
	b.discord.Close()
	b.db.Close()
	b.mu.Unlock()
}

// Register routes events in a guild to a corresponding service.
func (b *Bot) Register(guildID string, svc *Guild) {
	b.mu.Lock()
	b.guilds[guildID] = svc
	b.mu.Unlock()
}

// Unregister stops events in a from being routed to a service.
func (b *Bot) Unregister(guildID string) {
	b.mu.Lock()
	delete(b.guilds, guildID)
	b.mu.Unlock()
}

type boltGuildStorage struct {
	*bolt.DB
}

func newBoltGuildStorage(dbPath string) (*boltGuildStorage, error) {
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("guilds"))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte("songs"))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &boltGuildStorage{db}, nil
}

func (db boltGuildStorage) Get(guildID string) (GuildConfig, error) {
	info := GuildConfig{}
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val := bucket.Get([]byte(guildID))
		if val == nil {
			return errors.New("guild not found")
		}
		return json.Unmarshal(val, &info)
	})
	return info, err
}

func (db boltGuildStorage) Put(guildID string, info GuildConfig) error {
	val, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		return bucket.Put([]byte(guildID), val)
	})
}
