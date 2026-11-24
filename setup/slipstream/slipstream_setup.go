package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/arduino/go-apt-client"
	"github.com/gdamore/tcell/v2"
	t "github.com/rivo/tview"
	"gopkg.in/ini.v1"
)

func root() bool {
	return os.Geteuid() == 0
}

const cfgPath = "/etc/slipstream/slipstream.cfg"

func main() {
	// Check if program has been run with necessary perms
	if !root() {
		fmt.Fprintln(os.Stderr, "Please run this again with sudo")
		os.Exit(1)
	}

	app := t.NewApplication()
	var frame *t.Frame
	pages := t.NewPages()
	var form *t.Form
	var DockerModal, errModal *t.Modal
	var Port string
	errModal = t.NewModal().AddButtons([]string{"Okay"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Okay" {
			app.Stop()
		}
	})
	showErr := func(msg string) {
		errModal.SetText(msg)
		pages.SwitchToPage("errModal")
	}
	quitModal := t.NewModal().SetText("Are you sure you want to quit the slipstream setup?").AddButtons([]string{"yes", "no"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "yes" {
			app.Stop()
		} else if buttonLabel == "no" {
			pages.SwitchToPage("main")
		}
	})
	finishedModal := t.NewModal().SetText("Slipstream setup is complete. Please run the daemon to generate your secret key...").AddButtons([]string{"Understood"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Understood" {
			app.Stop()
		}
	})
	DockerModal = t.NewModal().SetText("Now, Lets install docker ( its purpose is to create and manage the servers on this node.) This Could Take a bit").AddButtons([]string{"Install Docker"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Install Docker" {
			_, err := apt.CheckForUpdates()
			if err != nil {
				showErr("Failed to install docker")
				return
			}
			_, err = apt.Install(&apt.Package{Name: "docker.io"}, &apt.Package{Name: "docker-compose-v2"})
			if err != nil {
				showErr("Failed to install docker")
				return
			}
			cmd := exec.Command("systemctl", "enable", "--now", "docker")
			err = cmd.Run()
			if err != nil {
				showErr("Failed to start docker")
				return
			}
			pages.SwitchToPage("finished")
		}
	})
	form = t.NewForm().
		AddInputField("Slipstream Service Port", "", 6, nil, nil).
		AddButton("Continue", func() {
			Port = (form.GetFormItemByLabel("Slipstream Service Port")).(*t.InputField).GetText()
			err := os.MkdirAll("/etc/slipstream", 0755)
			if err != nil {
				showErr("Failed to create slipstream directory")
				return
			}
			cfg, err := ini.LooseLoad(cfgPath)
			if err != nil {
				showErr("Failed to load slipstream cfg")
				return
			}
			cfg.Section("").Key("port").SetValue(Port)
			cfg.Section("").Key("isFirstRun").SetValue("true")
			err = cfg.SaveTo(cfgPath)
			if err != nil {
				showErr("Failed to save slipstream cfg")
				return
			}
			err = os.Chmod(cfgPath, 0660)
			if err != nil {
				showErr("Failed to set perms for slipstream cfg")
				return
			}
			pages.SwitchToPage("dockerInstall")

		}).
		AddButton("Quit", func() {
			pages.SwitchToPage("modal")
		})
	form.SetBorder(true).SetTitle("Slipstream Installation Wizard").SetTitleAlign(t.AlignLeft)
	frame = t.NewFrame(form).AddText("Navigate using the tab key. You can press ctrl+c at any time to exit the wizard", true, t.AlignCenter, tcell.ColorWhite)

	pages.AddPage("main", frame, true, true)
	pages.AddPage("modal", quitModal, true, false)
	pages.AddPage("finished", finishedModal, true, false)
	pages.AddPage("dockerInstall", DockerModal, true, false)
	pages.AddPage("errModal", errModal, true, false)
	err := app.SetRoot(pages, true).Run()
	if err != nil {
		panic(err)
	}
}
