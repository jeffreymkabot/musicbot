package status

import (
	"log"
	"sync"

	"github.com/bwmarrin/discordgo"
)

type View struct {
	// set once
	discord         *discordgo.Session
	buttons         []Button
	handlerRemovers []func()

	mu        sync.Mutex
	channelID string
	messageID string
}

type Button struct {
	Emoji  string
	Action func(userID string)
}

type State struct {
	ChannelID string
}

func NewView(discord *discordgo.Session, buttons []Button) *View {
	v := &View{
		discord: discord,
		buttons: buttons,
	}

	// better to make these once or each time the message is created?
	// if once then v will not be garbage collected until handlers removed
	// if each time message created then frequent interaction with session handler slices
	rmAddHandler := discord.AddHandler(v.reactionAddHandler)
	rmRemoveHandler := discord.AddHandler(v.reactionRemoveHandler)
	v.handlerRemovers = []func(){
		rmAddHandler,
		rmRemoveHandler,
	}

	return v
}

func (v *View) Render(channelID string, embed *discordgo.MessageEmbed) error {
	chID, msgID := v.getMessageRef()

	exists := chID != "" && msgID != ""
	differentChannel := chID != channelID
	tooFarBack := func() bool {
		msgs, err := v.discord.ChannelMessages(chID, 1, "", msgID, "")
		return err != nil || len(msgs) > 0
	}

	if exists && (differentChannel || tooFarBack()) {
		err := v.Clear()
		// retry once and move on
		if err != nil && v.Clear() != nil {
			log.Printf("failed to delete status message %v", err)
		}
		exists = false
	}

	if exists {
		_, err := v.discord.ChannelMessageEditEmbed(chID, msgID, embed)
		return err
	}

	msg, err := v.discord.ChannelMessageSendEmbed(channelID, embed)
	if err != nil {
		return err
	}

	v.setMessageRef(channelID, msg.ID)
	for _, button := range v.buttons {
		err := v.discord.MessageReactionAdd(channelID, msg.ID, button.Emoji)
		if err != nil {
			log.Printf("failed to attach button to status message %v", err)
		}
	}

	return nil
}

func (v *View) Clear() error {
	chID, msgID := v.getMessageRef()

	err := v.discord.ChannelMessageDelete(chID, msgID)
	if err == nil {
		v.setMessageRef("", "")
	}
	return err
}

func (v *View) Dispose() {
	v.Clear()
	for _, f := range v.handlerRemovers {
		f()
	}
}

func (v *View) getMessageRef() (string, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.channelID, v.messageID
}

func (v *View) setMessageRef(channelID, messageID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.channelID = channelID
	v.messageID = messageID
}

func (v *View) reactionAddHandler(_ *discordgo.Session, react *discordgo.MessageReactionAdd) {
	v.reactionHandler(react.MessageReaction)
}

func (v *View) reactionRemoveHandler(_ *discordgo.Session, react *discordgo.MessageReactionRemove) {
	v.reactionHandler(react.MessageReaction)
}

func (v *View) reactionHandler(react *discordgo.MessageReaction) {
	chID, msgID := v.getMessageRef()
	if react.ChannelID != chID || react.MessageID != msgID {
		return
	}

	member, err := v.discord.State.Member(react.GuildID, react.UserID)
	if err != nil || member.User.Bot {
		// if could not find member we could do v.discord.User(react.UserID)
		// but not sure if its possible for someone to react and not have them in state
		return
	}

	for _, btn := range v.buttons {
		if react.Emoji.Name == btn.Emoji {
			btn.Action(react.UserID)
		}
	}
}
