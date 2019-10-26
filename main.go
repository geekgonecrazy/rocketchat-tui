package main

import (
	"log"

	"github.com/RocketChat/Rocket.Chat.Go.SDK/models"
	tui "github.com/geekgonecrazy/tui-go"
)

type post struct {
	username string
	message  string
	time     string
}

var posts = []post{}

var ui tui.UI

var history *tui.Table
var titleBox *tui.Box

var sidebar *tui.Box
var channelList *tui.Table
var chat *tui.Box

var subscriptionList []models.ChannelSubscription

func main() {
	go connect()

	channelList = tui.NewTable(0, 0)

	channelScroll := tui.NewScrollArea(channelList)

	sidebar = tui.NewVBox(
		channelScroll,
	)

	sidebar.SetBorder(true)

	history = tui.NewTable(0, 0)

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

	theme := tui.NewTheme()
	theme.SetStyle("box.focused.border", tui.Style{Fg: tui.ColorYellow, Bg: tui.ColorDefault})
	theme.SetStyle("table.cell.selected", tui.Style{Fg: tui.ColorYellow})

	ui.SetTheme(theme)

	type tabListMap struct {
		widget tui.Widget
		table  *tui.Table
		fn     func(int)
	}

	tabList := []tabListMap{
		{
			input,
			nil,
			func(i int) {},
		},
		{
			channelScroll,
			channelList,
			changeSelectedChannel,
		},
		{
			historyScroll,
			history,
			func(i int) {},
		},
	}

	focused := 0

	ui.SetKeybinding("Esc", func() { ui.Quit() })

	ui.SetKeybinding("Tab", func() {
		next := 0

		for i, t := range tabList {
			if t.widget.IsFocused() {
				// if less then the length go next
				if i < len(tabList)-1 {
					next = i + 1
				} else {
					next = 0
				}

				t.widget.SetFocused(false)

				if t.table != nil {
					t.table.SetSelected(-1)
				}
			}
		}

		tabList[next].widget.SetFocused(true)

		if tabList[next].table != nil {
			tabList[next].table.SetSelected(0)
		}

		focused = next
	})

	ui.SetKeybinding("Up", func() {
		if tabList[focused].table == nil {
			return
		}

		if tabList[focused].table.Selected() > 0 {
			tabList[focused].table.Select(tabList[focused].table.Selected() - 1)
		}
	})

	ui.SetKeybinding("Down", func() {
		if tabList[focused].table == nil {
			return
		}

		if tabList[focused].table.Selected() < tabList[focused].table.Grid.Length()-1 {
			tabList[focused].table.Select(tabList[focused].table.Selected() + 1)
		}
	})

	ui.SetKeybinding("Enter", func() {
		if tabList[focused].table == nil {
			return
		}

		tabList[focused].fn(tabList[focused].table.Selected())
	})

	ui.SetKeybinding("Shift+Up", func() {
		changeSelectedChannel(0)
	})

	ui.SetKeybinding("Alt+Up", func() {
		historyBox.SetFocused(true)
		/*if history.Selected() > 0 {
			history.Select(history.Selected() - 1)
		}*/
	})

	ui.SetKeybinding("Alt+Down", func() {
		/*if history.Selected() < len(messageHistory)-1 {
			history.Select(history.Selected() + 1)
		}*/
	})

	if err := ui.Run(); err != nil {
		log.Fatal(err)
	}
}
