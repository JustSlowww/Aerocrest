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

const cfgPath = "/etc/aerocrest/aerocrest.cfg"

func main() {
	if !root() {
		fmt.Fprintln(os.Stderr, "Please run this again with sudo")
		os.Exit(1)
	}
	app := t.NewApplication()
	var frame *t.Frame
	pages := t.NewPages()
	var form *t.Form
	var dockerInstall, cfgSetup, finishedModal, errModal *t.Modal
	// Form Values
	var port, dbPort, dbName, dbPassword string
	// Modals
	errModal = t.NewModal().AddButtons([]string{"Okay"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Okay" {
			app.Stop()
		}
	})
	showErr := func(msg string) {
		errModal.SetText(msg)
		pages.SwitchToPage("errModal")
	}
	quitModal := t.NewModal().SetText("Are you sure you want to quit the aerocrest setup?").AddButtons([]string{"yes", "no"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "yes" {
			app.Stop()
		} else if buttonLabel == "no" {
			pages.SwitchToPage("main")
		}
	})
	dockerInstall = t.NewModal().SetText("Now, Lets install docker and create the database ( its purpose here is to create and run the postgres database) This Could Take a bit").AddButtons([]string{"Install Docker"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
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
			cmd = exec.Command("docker", "run", "--name", dbName, "-e", "POSTGRES_PASSWORD="+dbPassword, "-e", "POSTGRES_DB="+dbName, "-p", "127.0.0.1:"+dbPort+":5432", "-v", dbName+"-data:/var/lib/postgresql", "--restart", "unless-stopped", "-d", "postgres:18")
			bytes, err := cmd.CombinedOutput()
			if err != nil {
				showErr("Failed to create db, " + string(bytes))
				return
			}
			pages.SwitchToPage("cfgSetup")
		}
	})
	cfgSetup = t.NewModal().SetText("Finally, lets setup your config file located at /etc/aerocrest/aerocrest.cfg").AddButtons([]string{"Setup config file"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Setup config file" {
			err := os.MkdirAll("/etc/aerocrest", 0755)
			if err != nil {
				showErr("Failed to create aerocrest directory")
				return
			}
			cfg, err := ini.LooseLoad(cfgPath)
			if err != nil {
				showErr("Failed to Load or Create config file...")
				return
			}
			cfg.Section("").Key("port").SetValue(port)
			cfg.Section("").Key("dbName").SetValue(dbName)
			cfg.Section("").Key("dbPort").SetValue(dbPort)
			cfg.Section("").Key("dbPass").SetValue(dbPassword)
			err = cfg.SaveTo(cfgPath)
			if err != nil {
				showErr("Failed to Save config file...")
				return
			}
			err = os.Chmod(cfgPath, 0600)
			if err != nil {
				showErr("Failed to assign this files the necessary perms...")
				return
			}
			pages.SwitchToPage("finish")
		}
	})
	finishedModal = t.NewModal().SetText("Aerocrest setup is complete!").AddButtons([]string{"Understood"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Understood" {
			app.Stop()
		}
	})
	// NOTE: Add NGINX setup later
	// form and border
	form = t.NewForm().
		AddInputField("Aerocrest Panel Port", "", 6, nil, nil).
		AddInputField("Database Port", "", 6, nil, nil).
		AddInputField("Database Name", "", 40, nil, nil).
		AddPasswordField("Database Password", "", 40, '*', nil).
		AddButton("Continue", func() {
			port = (form.GetFormItemByLabel("Aerocrest Panel Port")).(*t.InputField).GetText()
			dbPort = (form.GetFormItemByLabel("Database Port")).(*t.InputField).GetText()
			dbName = (form.GetFormItemByLabel("Database Name")).(*t.InputField).GetText()
			dbPassword = (form.GetFormItemByLabel("Database Password")).(*t.InputField).GetText()
			pages.SwitchToPage("docker")
		}).
		AddButton("Quit", func() {
			pages.SwitchToPage("modal")
		})

	form.SetBorder(true).SetTitle("Aerocrest Installation Wizard")
	frame = t.NewFrame(form).AddText("Navigate using the tab key. You can press esc at any time to exit the wizard", true, t.AlignCenter, tcell.ColorWhite)

	// pages
	pages.AddPage("main", frame, true, true)
	pages.AddPage("modal", quitModal, true, false)
	pages.AddPage("docker", dockerInstall, true, false)
	pages.AddPage("cfgSetup", cfgSetup, true, false)
	pages.AddPage("finish", finishedModal, true, false)
	pages.AddPage("errModal", errModal, true, false)
	err := app.SetRoot(pages, true).Run()
	if err != nil {
		panic(err)
	}
}
