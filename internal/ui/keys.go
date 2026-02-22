package ui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Submit key.Binding
	Quit   key.Binding
}

var keys = keyMap{
	Submit: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+d", "ctrl+c"),
		key.WithHelp("ctrl+d", "quit"),
	),
}
