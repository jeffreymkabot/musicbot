package music

import (
	"errors"
	"log"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dgv "github.com/jeffreymkabot/discordvoice"
)

const defaultPrefix = "#!"

type guild struct {
	mu      sync.RWMutex
	guildID string
	state   int
	play    *dgv.Player
	guildInfo
}

type guildInfo struct {
	Prefix         string   `json:"prefix"`
	ListenChannels []string `json:"listen"`
}

type BotOption func(*Bot)

func Soundcloud(clientID string) BotOption {
	return func(b *Bot) {
		if clientID != "" {
			log.Printf("activate soundcloud")
			b.soundcloud = clientID
			b.commands = append(b.commands, soundcloud)
		}
	}
}

type Bot struct {
	mu         sync.RWMutex
	session    *discordgo.Session
	db         *bolt.DB
	owner      string
	soundcloud string
	guilds     map[string]*guild
	commands   []*command
}

func New(token string, dbPath string, owner string, opts ...BotOption) (*Bot, error) {
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

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	b := &Bot{
		session: session,
		db:      db,
		owner:   owner,
		guilds:  make(map[string]*guild),
		commands: []*command{
			// help,
			youtube,
			skip,
			pause,
			// clear,
			// setPrefix,
			setListen,
			unsetListen,
		},
	}

	for _, opt := range opts {
		opt(b)
	}

	log.Printf("available commands %#v", b.commands)

	session.AddHandlerOnce(onReady(b))

	err = session.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	b.session.Close()
	b.db.Close()
	b.mu.Unlock()
}

func (b *Bot) exec(cmd *command, g *guild, authorID string, textChannelID string, args []string) error {
	g.mu.RLock()
	if cmd.isListenChannelOnly && !contains(g.ListenChannels, textChannelID) {
		g.mu.RUnlock()
		log.Printf("command invoked in unregistered channel")
		return nil
	}
	g.mu.RUnlock()

	if cmd.isOwnerOnly && b.owner != authorID {
		return errors.New("user not allowed to execute this command")
	}

	log.Printf("exec command %v in %v with %v\n", cmd.name, g.guildID, args)
	return cmd.run(b, g, textChannelID, args)
}

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}
