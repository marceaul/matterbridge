package bmatrix

import (
	"regexp"
	"sync"

	"github.com/42wim/matterbridge/bridge/config"
	log "github.com/Sirupsen/logrus"
	matrix "github.com/matrix-org/gomatrix"
)

type Bmatrix struct {
	mc      *matrix.Client
	Config  *config.Protocol
	Remote  chan config.Message
	Account string
	UserID  string
	RoomMap map[string]string
	sync.RWMutex
}

var flog *log.Entry
var protocol = "matrix"

func init() {
	flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *Bmatrix {
	b := &Bmatrix{}
	b.RoomMap = make(map[string]string)
	b.Config = &cfg
	b.Account = account
	b.Remote = c
	return b
}

func (b *Bmatrix) Connect() error {
	var err error
	flog.Infof("Connecting %s", b.Config.Server)
	b.mc, err = matrix.NewClient(b.Config.Server, "", "")
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	resp, err := b.mc.Login(&matrix.ReqLogin{
		Type:     "m.login.password",
		User:     b.Config.Login,
		Password: b.Config.Password,
	})
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	b.mc.SetCredentials(resp.UserID, resp.AccessToken)
	b.UserID = resp.UserID
	flog.Info("Connection succeeded")
	go b.handlematrix()
	return nil
}

func (b *Bmatrix) Disconnect() error {
	return nil
}

func (b *Bmatrix) JoinChannel(channel config.ChannelInfo) error {
	resp, err := b.mc.JoinRoom(channel.Name, "", nil)
	if err != nil {
		return err
	}
	b.Lock()
	b.RoomMap[resp.RoomID] = channel.Name
	b.Unlock()
	return err
}

func (b *Bmatrix) Send(msg config.Message) (string, error) {
	flog.Debugf("Receiving %#v", msg)
	// ignore delete messages
	if msg.Event == config.EVENT_MSG_DELETE {
		return "", nil
	}
	channel := b.getRoomID(msg.Channel)
	flog.Debugf("Sending to channel %s", channel)
	if msg.Event == config.EVENT_USER_ACTION {
		b.mc.SendMessageEvent(channel, "m.room.message",
			matrix.TextMessage{"m.emote", msg.Username + msg.Text})
		return "", nil
	}
	b.mc.SendText(channel, msg.Username+msg.Text)
	return "", nil
}

func (b *Bmatrix) getRoomID(channel string) string {
	b.RLock()
	defer b.RUnlock()
	for ID, name := range b.RoomMap {
		if name == channel {
			return ID
		}
	}
	return ""
}
func (b *Bmatrix) handlematrix() error {
	syncer := b.mc.Syncer.(*matrix.DefaultSyncer)
	syncer.OnEventType("m.room.message", func(ev *matrix.Event) {
		if (ev.Content["msgtype"].(string) == "m.text" || ev.Content["msgtype"].(string) == "m.notice" || ev.Content["msgtype"].(string) == "m.emote") && ev.Sender != b.UserID {
			b.RLock()
			channel, ok := b.RoomMap[ev.RoomID]
			b.RUnlock()
			if !ok {
				flog.Debugf("Unknown room %s", ev.RoomID)
				return
			}
			username := ev.Sender[1:]
			if b.Config.NoHomeServerSuffix {
				re := regexp.MustCompile("(.*?):.*")
				username = re.ReplaceAllString(username, `$1`)
			}
			rmsg := config.Message{Username: username, Text: ev.Content["body"].(string), Channel: channel, Account: b.Account, UserID: ev.Sender}
			if ev.Content["msgtype"].(string) == "m.emote" {
				rmsg.Event = config.EVENT_USER_ACTION
			}
			flog.Debugf("Sending message from %s on %s to gateway", ev.Sender, b.Account)
			b.Remote <- rmsg
		}
		flog.Debugf("Received: %#v", ev)
	})
	go func() {
		for {
			if err := b.mc.Sync(); err != nil {
				flog.Println("Sync() returned ", err)
			}
		}
	}()
	return nil
}
