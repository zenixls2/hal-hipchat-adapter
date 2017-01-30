package hal_hipchat_adapter

import (
	"fmt"
	"github.com/daneharrigan/hipchat"
	"github.com/danryan/env"
	"github.com/danryan/hal"
	"regexp"
	"strings"
)

func init() {
	hal.RegisterAdapter("hipchat", New)
}

// adapter struct
type adapter struct {
	hal.BasicAdapter
	user     string
	nick     string
	name     string
	password string
	resource string
	rooms    []string
	client   *hipchat.Client
	// config   *config
}

type config struct {
	User     string `env:"required key=HAL_HIPCHAT_USER"`
	Password string `env:"required key=HAL_HIPCHAT_PASSWORD"`
	Rooms    string `env:"key=HAL_HIPCHAT_ROOMS"`
	Resource string `env:"key=HAL_HIPCHAT_RESOURCE default=bot"`
}

// New returns an initialized adapter
func New(robot *hal.Robot) (hal.Adapter, error) {
	c := &config{}
	env.MustProcess(c)

	a := &adapter{
		user:     c.User,
		password: c.Password,
		resource: c.Resource,
		rooms: func() []string {
			if len(c.Rooms) > 0 {
				return strings.Split(c.Rooms, ",")
			} else {
				return []string{}
			}
		}(),
	}
	robot.Adapter = a
	a.SetRobot(robot)
	return a, nil
}

// Run starts the adapter
func (a *adapter) Run() error {
	go a.startConnection()
	return nil
}

// Stop shuts down the adapter
func (a *adapter) Stop() error {
	// hipchat package doesn't provide an explicit stop command
	return nil
}

// Send sends a regular response
func (a *adapter) Send(res *hal.Response, strings ...string) error {
	for _, str := range strings {
		hal.Logger.Debug(res.Message.Room)
		user, err := a.Robot.Users.Get(res.Message.Room)
		if err != nil {
			// cannot find RoomID in Users, so this should be a real room
			a.client.Say(res.Message.Room, a.name, str)
		} else {
			// Found user, we should send using private msg
			a.client.PrivSay(user.ID, a.name, str)
		}
	}
	return nil
}

// Reply sends a direct response
func (a *adapter) Reply(res *hal.Response, strings ...string) error {
	hal.Logger.Debug("resultrply: ", strings)
	newStrings := make([]string, len(strings))
	for _, str := range strings {
		s := fmt.Sprintf("@%s: %s", mentionName(res.Envelope.User), str)
		newStrings = append(newStrings, s)
	}

	return a.Send(res, newStrings...)
}

// Emote is not implemented.
func (a *adapter) Emote(res *hal.Response, strings ...string) error {
	return nil
}

// Topic is not implemented.
func (a *adapter) Topic(res *hal.Response, strings ...string) error {
	return nil
}

// Play is not implemented.
func (a *adapter) Play(res *hal.Response, strings ...string) error {
	return nil
}

// Receive forwards a message to the robot
func (a *adapter) Receive(msg *hal.Message) error {
	hal.Logger.Debug("hipchat - adapter received message")
	// this is part of the code for debuging purpose extract from hal.
	resp := hal.NewResponseFromMessage(a.Robot, msg)
	for _, h := range a.Robot.Handlers() {
		respondRegexpTemplate := fmt.Sprintf(`^(?:@?(?:%s|%s)[:,]?)\s+(?:${1})`, hal.Config.Alias, hal.Config.Name)
		reg := regexp.MustCompile(strings.Replace(respondRegexpTemplate, "${1}", h.(*hal.Handler).Pattern, 1))
		hal.Logger.Debug((h.(*hal.Handler)).Pattern, resp.Message.Text, reg.FindAllStringSubmatch(resp.Message.Text, -1))
	}
	a.Robot.Receive(msg)
	hal.Logger.Debug("hipchat - adapter sent message to robot")

	return nil
}

func (a *adapter) newMessage(msg *hipchat.Message) *hal.Message {
	hal.Logger.Debug(msg)
	from := strings.Split(msg.From, "/")
	user, _ := a.Robot.Users.Get(from[0])
	// notice there's a difference for Handler and FullHandler in regexp.
	// they use different templates.
	// need to implement something else to fetch only body to
	// user defined methods
	hal.Logger.Debugf("User: %v, Room: %s, Text: %s\n",
		user,
		from[0],
		"@"+hal.Config.Alias+": "+msg.Body)

	return &hal.Message{
		User: user,
		Room: from[0],
		Text: "@" + hal.Config.Alias + ": " + msg.Body,
	}
}

func mentionName(u *hal.User) string {
	mn, ok := u.Options["mentionName"]
	if !ok {
		return ""
	}
	return mn.(string)
}

func (a *adapter) startConnection() error {
	client, err := hipchat.NewClient(a.user, a.password, a.resource)
	if err != nil {
		hal.Logger.Error(err.Error())
		return err
	}

	client.Status("chat")
	hal.Logger.Debug("client Id", client.Id)
	client.RequestUsers()
	for _, user := range <-client.Users() {
		// retrieve the name and mention name of our bot from the server
		if user.Id == client.Id {
			a.name = user.Name
			a.nick = user.MentionName
			// skip adding the bot to the users map
			continue
		}
		// Initialize a newUser object in case we need it.
		newUser := hal.User{
			ID:   user.Id,
			Name: user.Name,
			Options: map[string]interface{}{
				"mentionName": user.MentionName,
			},
		}
		hal.Logger.Debugf("found User %v", newUser)
		a.Robot.Users.Set(user.Id, newUser)
		if _, err := a.Robot.Users.Get(user.Id); err != nil {
			panic(fmt.Sprintf("User add fail: %v", user))
		}
	}

	// Make a map of room JIDs to human names
	client.RequestRooms()
	_rooms := <-client.Rooms()
	roomJids := make(map[string]string, len(_rooms))
	for _, room := range _rooms {
		roomJids[room.Name] = room.Id
		hal.Logger.Debugf("found Room %s : %s", room.Name, room.Id)
	}
	client.Status("chat")
	// Only join the rooms we want
	if len(a.rooms) > 0 {
		for name, room := range a.rooms {
			hal.Logger.Debugf("%s - joined %s", a, name)
			client.Join(roomJids[room], a.name)
		}
	} else {
		for name, room := range roomJids {
			hal.Logger.Debugf("%s - joined %s", a, name)
			client.Join(room, a.name)
		}
	}

	a.client = client
	a.Robot.Alias = a.nick

	// send an empty string every 60 seconds so hipchat doesn't disconnect us
	go client.KeepAlive()

	for message := range client.Messages() {
		hal.Logger.Debugf("msg %v", message)
		from := strings.Split(message.From, "/")
		// ignore messages directly from the channel
		// TODO: don't do this :)
		if len(from) < 2 {
			continue
		}
		// ingore messages from our bot
		if from[1] == a.name {
			continue
		}

		msg := a.newMessage(message)
		a.Receive(msg)
	}
	return nil
}
