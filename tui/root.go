package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pkg/browser"
	"github.com/pomdtr/sunbeam/app"
)

type Config struct {
	RootItems []app.RootItem `yaml:"rootItems"`
	Env       map[string][]string
}

type Page interface {
	Init() tea.Cmd
	Update(tea.Msg) (Page, tea.Cmd)
	View() string
	SetSize(width, height int)
}

type Model struct {
	width, height int

	root  Page
	pages []Page

	hidden bool
	exit   bool
}

func NewModel(root Page) *Model {
	return &Model{root: root}
}

func (m *Model) SetRoot(root Page) {
	m.root = root
}

func (m *Model) Init() tea.Cmd {
	return m.root.Init()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape:
			fmt.Sprintln("Escape")
		case tea.KeyCtrlC:
			m.hidden = true
			m.exit = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case OpenUrlMsg:
		err := browser.OpenURL(msg.Url)
		if err != nil {
			return m, NewErrorCmd(err)
		}

		m.hidden = true
		return m, tea.Quit
	case CopyTextMsg:
		err := clipboard.WriteAll(msg.Text)
		if err != nil {
			return m, NewErrorCmd(fmt.Errorf("failed to copy text to clipboard: %s", err))
		}

		m.hidden = true
		return m, tea.Quit
	case PushPageMsg:
		cmd := m.Push(msg.Page)
		return m, cmd
	case pushMsg:
		cmd := m.Push(msg.container)
		return m, cmd
	case popMsg:
		if len(m.pages) == 0 {
			return m, tea.Quit
		} else {
			m.Pop()
			return m, nil
		}
	case error:
		detail := NewDetail("Error")
		detail.SetSize(m.width, m.pageHeight())
		detail.viewport.SetContent(msg.Error())

		if len(m.pages) == 0 {
			m.root = detail
		} else {
			m.pages[len(m.pages)-1] = detail
		}

		return m, detail.Init()
	}

	// Update the current page
	var cmd tea.Cmd

	if len(m.pages) == 0 {
		m.root, cmd = m.root.Update(msg)
	} else {
		currentPageIdx := len(m.pages) - 1
		m.pages[currentPageIdx], cmd = m.pages[currentPageIdx].Update(msg)
	}

	return m, cmd
}

func (m *Model) View() string {
	if m.hidden {
		return ""
	}

	if len(m.pages) > 0 {
		currentPage := m.pages[len(m.pages)-1]
		return currentPage.View()
	}

	return m.root.View()

}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height

	m.root.SetSize(width, m.pageHeight())
	for _, page := range m.pages {
		page.SetSize(m.width, m.pageHeight())
	}
}

func (m *Model) pageHeight() int {
	// if m.config.Height > 0 {
	// 	return utils.Min(m.config.Height, m.height)
	// }
	return m.height
}

type popMsg struct{}

func PopCmd() tea.Msg {
	return popMsg{}
}

type pushMsg struct {
	container Page
}

func NewPushCmd(c Page) tea.Cmd {
	return func() tea.Msg {
		return pushMsg{c}
	}
}

func (m *Model) Push(page Page) tea.Cmd {
	page.SetSize(m.width, m.pageHeight())
	m.pages = append(m.pages, page)
	return page.Init()
}

func (m *Model) Pop() {
	if len(m.pages) > 0 {
		m.pages = m.pages[:len(m.pages)-1]
	}
}

func loadHistory(historyPath string) map[string]int64 {
	history := make(map[string]int64)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		return history
	}

	json.Unmarshal(data, &history)
	return history
}

type RootItemWithID struct {
	app.RootItem
	id string
}

func NewRootList(extensionMap map[string]app.Extension, additionalItems ...app.RootItem) Page {
	stateDir := path.Join(os.Getenv("HOME"), ".local", "state", "sunbeam")
	historyPath := path.Join(stateDir, "history.json")
	history := loadHistory(historyPath)
	list := NewList("Sunbeam")
	list.filter.Less = func(i, j FilterItem) bool {
		iValue, ok := history[i.ID()]
		if !ok {
			iValue = 0
		}
		jValue, ok := history[j.ID()]
		if !ok {
			jValue = 0
		}

		return iValue > jValue
	}

	rootItems := make([]RootItemWithID, 0)
	for extensionName, extension := range extensionMap {
		for _, rootItem := range extension.RootItems {
			rootItem.Extension = extensionName
			rootItems = append(rootItems, RootItemWithID{
				RootItem: rootItem,
				id:       fmt.Sprintf("%s:%s", extensionName, rootItem.Title),
			})
		}
	}

	for _, item := range additionalItems {
		if _, ok := extensionMap[item.Extension]; !ok {
			continue
		}
		rootItems = append(rootItems, RootItemWithID{
			RootItem: item,
			id:       fmt.Sprintf("config:%s", item.Title),
		})
	}

	listItems := make([]ListItem, 0)
	for _, rootItem := range rootItems {
		rootItem := rootItem
		extension := extensionMap[rootItem.Extension]
		listItems = append(listItems, ListItem{
			Id:          rootItem.id,
			Title:       rootItem.Title,
			Subtitle:    extension.Title,
			Accessories: []string{rootItem.Extension},
			Actions: []Action{
				{
					Title:    "Run Command",
					Shortcut: "enter",
					Cmd: func() tea.Msg {
						history[rootItem.id] = time.Now().Unix()
						if _, err := os.Stat(stateDir); os.IsNotExist(err) {
							os.MkdirAll(stateDir, 0755)
						}

						data, _ := json.Marshal(history)
						os.WriteFile(historyPath, data, 0644)

						return PushPageMsg{
							Page: NewCommandRunner(
								NamedExtension{
									Name:      rootItem.Extension,
									Extension: extension,
								},
								NamedCommand{
									Name:    rootItem.Command,
									Command: extension.Commands[rootItem.Command],
								},
								rootItem.With,
								[]string{},
							),
						}

					},
				},
			},
		})
	}

	list.SetItems(listItems)

	return list
}

func Draw(model *Model, fullscreen bool) (err error) {
	// Log to a file
	if env := os.Getenv("SUNBEAM_LOG_FILE"); env != "" {
		f, err := tea.LogToFile(env, "debug")
		if err != nil {
			log.Fatalf("could not open log file: %v", err)
		}
		defer f.Close()
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		logDir := path.Join(home, ".local", "state", "sunbeam")
		if _, err := os.Stat(logDir); os.IsNotExist(err) {
			err = os.MkdirAll(path.Join(home, ".local", "state", "sunbeam"), 0755)
			if err != nil {
				return err
			}
		}
		tea.LogToFile(path.Join(logDir, "sunbeam.log"), "")
	}

	// Disable the background detection since we are only using ANSI colors
	lipgloss.SetHasDarkBackground(true)

	var p *tea.Program
	if fullscreen {
		p = tea.NewProgram(model, tea.WithAltScreen())
	} else {
		p = tea.NewProgram(model)
	}

	_, err = p.Run()
	if err != nil {
		return err
	}

	return nil
}
