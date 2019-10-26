package main

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/RocketChat/Rocket.Chat.Go.SDK/models"
	"github.com/RocketChat/Rocket.Chat.Go.SDK/realtime"
	"github.com/RocketChat/Rocket.Chat.Go.SDK/rest"
	"github.com/geekgonecrazy/tui-go"
	wordwrap "github.com/mitchellh/go-wordwrap"
)

var rlClient *realtime.Client
var restClient *rest.Client

var msgChannel chan models.Message

var subscribed = make(map[string]string)
var messageHistory []models.Message

var activeChannel models.ChannelSubscription

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

func changeSelectedChannel(index int) {
	activeChannel = subscriptionList[index]

	titleBox.Remove(0)

	titleBox.Append(tui.NewLabel(activeChannel.Name))

	history.RemoveRows()

	messageHistory = []models.Message{}

	if _, ok := subscribed[activeChannel.RoomId]; !ok {
		if err := rlClient.SubscribeToMessageStream(&models.Channel{ID: activeChannel.RoomId}, msgChannel); err != nil {
			log.Println(err)
		}

		subscribed[activeChannel.RoomId] = activeChannel.RoomId
	}

	loadHistory()
}

func handleMessageStream() {

	for {
		message := <-msgChannel

		if message.RoomID != activeChannel.RoomId {
			continue
		}

		messageHistory = append(messageHistory, message)

		text := message.Msg

		if text == "" {
			text = message.Text
		}

		ui.Update(func() {

			line := fmt.Sprintf("%s <%s> %s", message.Timestamp.Format("15:04"), message.User.UserName, text)

			// couldn't get the automatic linewrapping to function right
			lineNewlines := wordwrap.WrapString(line, uint(chat.Size().X))
			linesSplit := strings.Split(lineNewlines, "\n")

			box := tui.NewVBox()

			for _, l := range linesSplit {
				box.Append(tui.NewLabel(l))
			}

			history.AppendRow(box)
		})
	}
}

func sendMessage(text string) {

	channelId := activeChannel.RoomId

	if _, err := rlClient.SendMessage(&models.Message{RoomID: channelId, Msg: text}); err != nil {
		log.Println(err)
	}
}

func loadHistory() {
	channelId := activeChannel.RoomId

	messages, err := rlClient.LoadHistory(channelId)
	if err != nil {
		log.Println(err)
	}

	// Reverse order so will show up properly
	for i := len(messages)/2 - 1; i >= 0; i-- {
		opp := len(messages) - 1 - i
		messages[i], messages[opp] = messages[opp], messages[i]
	}

	for _, message := range messages {
		msgChannel <- message
	}

}

func getSubscriptions() {

	subscriptions, err := rlClient.GetChannelSubscriptions()
	if err != nil {
		panic(err)
	}

	ui.Update(func() {
		for _, sub := range subscriptions {
			if sub.Open && sub.Name != "" {
				channelList.AppendRow(
					tui.NewLabel(fmt.Sprintf("[%s] %s", sub.Type, sub.Name)),
				)

				subscriptionList = append(subscriptionList, sub)
			}
		}
	})
}
