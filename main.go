package main

import (
	"log"

	"github.com/RocketChat/Rocket.Chat.Go.SDK/models"
	"github.com/marcusolsson/tui-go"
)

type post struct {
	username string
	message  string
	time     string
}

var posts = []post{}

var ui tui.UI

var history *tui.List
var titleBox *tui.Box

var sidebar *tui.Box
var channelList *tui.Table
var chat *tui.Box

var subscriptionList []models.ChannelSubscription
var selectedChannel = 0

func main() {
	go connect()

	channelList = tui.NewTable(0, 0)

	sidebar = tui.NewVBox(
		channelList,
	)

	sidebar.SetBorder(true)

	history = tui.NewList()

	historyScroll := tui.NewScrollArea(history)
	historyScroll.SetAutoscrollToBottom(true)

	historyBox := tui.NewVBox(historyScroll)
	historyBox.SetSizePolicy(tui.Preferred, tui.Expanding)
	historyBox.SetBorder(true)

	titleBox = tui.NewHBox()
	titleBox.SetSizePolicy(tui.Minimum, tui.Maximum)
	titleBox.SetBorder(true)

	input := tui.NewEntry()
	input.SetFocused(true)
	input.SetSizePolicy(tui.Expanding, tui.Maximum)

	inputBox := tui.NewHBox(input)
	inputBox.SetBorder(true)
	inputBox.SetSizePolicy(tui.Expanding, tui.Maximum)

	chat = tui.NewVBox(titleBox, historyBox, inputBox)
	chat.SetSizePolicy(tui.Expanding, tui.Expanding)

	input.OnSubmit(func(e *tui.Entry) {
		sendMessage(e.Text())
		input.SetText("")
	})

	root := tui.NewHBox(sidebar, chat)

	uI, err := tui.New(root)
	if err != nil {
		log.Fatal(err)
	}

	ui = uI

	ui.SetKeybinding("Esc", func() { ui.Quit() })
	ui.SetKeybinding("Up", func() { selectedChannel--; channelList.Select(selectedChannel) })
	ui.SetKeybinding("Down", func() { selectedChannel++; channelList.Select(selectedChannel) })
	ui.SetKeybinding("Shift+Up", func() {
		changeSelectedChannel()
	})

	if err := ui.Run(); err != nil {
		log.Fatal(err)
	}
}
