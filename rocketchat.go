package main

import (
	"fmt"
	"image"
	"log"
	"net/url"

	"github.com/RocketChat/Rocket.Chat.Go.SDK/models"
	"github.com/RocketChat/Rocket.Chat.Go.SDK/realtime"
	"github.com/RocketChat/Rocket.Chat.Go.SDK/rest"
	"github.com/marcusolsson/tui-go"
)

var rlClient *realtime.Client
var restClient *rest.Client

var msgChannel chan models.Message

var subscribed = make(map[string]string)
var messageHistory []models.Message

var email = ""
var password = ""

func connect() error {
	serverUrl, err := url.Parse("https://open.rocket.chat")
	if err != nil {
		return err
	}

	c, err := realtime.NewClient(serverUrl, false)
	if err != nil {
		log.Println("Failed to connect", err)
		return err
	}

	rlClient = c

	_, err = c.Login(&models.UserCredentials{Email: email, Password: password})
	if err != nil {
		return err
	}

	c2 := rest.NewClient(serverUrl, false)

	restClient = c2

	if err := restClient.Login(&models.UserCredentials{Email: email, Password: password}); err != nil {
		log.Println("failed to login")
		return err
	}

	msgChannel = make(chan models.Message, 100)

	getSubscriptions()

	handleMessageStream()

	return nil
}

func changeSelectedChannel() {
	sub := subscriptionList[selectedChannel]

	channelList.SetCell(image.Point{0, selectedChannel}, tui.NewLabel(fmt.Sprintf("*[%s] %s", sub.Type, sub.Name)))

	titleBox.Remove(0)

	titleBox.Append(tui.NewLabel(sub.Name))

	for i := 0; i < len(messageHistory); i++ {
		log.Println("booga booga", i)
		history.Remove(i)
	}

	messageHistory = []models.Message{}

	if _, ok := subscribed[sub.RoomId]; !ok {
		if err := rlClient.SubscribeToMessageStream(&models.Channel{ID: sub.RoomId}, msgChannel); err != nil {
			log.Println(err)
		}
		subscribed[sub.RoomId] = sub.RoomId
	}

	loadHistory()
}

func handleMessageStream() {

	/*channelId, err := rlClient.GetChannelId("cli-test-test")
	if err != nil {
		panic(err)
	}*/

	for {
		message := <-msgChannel

		if message.RoomID != subscriptionList[selectedChannel].RoomId {
			//log.Println("got message for channel not in")
			continue
		}

		messageHistory = append(messageHistory, message)

		text := message.Msg

		if text == "" {
			text = message.Text
		}

		if len(message.Attachments) > 0 {
			for _, attachment := range message.Attachments {
				if attachment.ImageURL != "" {
					text += fmt.Sprintf(" <%s>", attachment.ImageURL)
				}

				if attachment.TitleLink != "" {
					text += fmt.Sprintf(" <%s>", attachment.TitleLink)
				}

				if attachment.VideoURL != "" {
					text += fmt.Sprintf(" <%s>", attachment.VideoURL)
				}

				if attachment.ThumbURL != "" {
					text += fmt.Sprintf(" <%s>", attachment.ThumbURL)
				}
			}
		}

		ui.Update(func() {
			history.Append(tui.NewHBox(
				tui.NewLabel(message.Timestamp.Format("15:04")),
				tui.NewPadder(1, 0, tui.NewLabel(fmt.Sprintf("<%s>", message.User.UserName))),
				tui.NewLabel(text),
				tui.NewSpacer(),
			))
		})
	}
}

func sendMessage(text string) {
	/*channelId, err := rlClient.GetChannelId("cli-test-test")
	if err != nil {
		panic(err)
	}*/

	channelId := subscriptionList[selectedChannel].RoomId

	if _, err := rlClient.SendMessage(&models.Message{RoomID: channelId, Msg: text}); err != nil {
		log.Println(err)
	}
}

func loadHistory() {
	/*channelId, err := rlClient.GetChannelId("cli-test-test")
	if err != nil {
		panic(err)
	}*/

	channelId := subscriptionList[selectedChannel].RoomId

	messages, err := rlClient.LoadHistory(channelId)
	if err != nil {
		log.Println(err)
	}

	//fmt.Printf("%+v", messages)

	for _, message := range messages {
		msgChannel <- message
	}

}

func getSubscriptions() {

	subscriptions, err := rlClient.GetChannelSubscriptions()
	if err != nil {
		panic(err)
	}

	subscriptionList = subscriptions

	ui.Update(func() {
		for _, sub := range subscriptions {
			channelList.AppendRow(
				tui.NewLabel(fmt.Sprintf("[%s] %s", sub.Type, sub.Name)),
			)
		}
	})

	/*tui.NewLabel("CHANNELS"),
	tui.NewLabel("general"),
	tui.NewLabel(""),
	tui.NewLabel("DIRECT MESSAGES"),
	tui.NewLabel("aaron.ogle"),
	tui.NewSpacer()*/
}
