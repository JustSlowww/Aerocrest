<h1 align="center">Aerocrest</h1>
<h4 align="center">A Fast Hosting Management Panel Made with GO, chi and go's HTML template package</h4>

### What is aerocrest ?

Aerocrest similarly to pterodactyl or jexpanel is a hosting management panel. Connect your nodes using slipstream and on the panel, users can create their own game/web servers on said nodes.

> [!NOTE]
> 
> Aerocrest currently only supports Debian based Linux distros
> 
> Additionally, at the current stage aerocrest is not production ready but still a work in progress
>
> A small amount of ai was used to learn and understand sql queries at first.

### Tech Stack:

- Go 1.27.1
- Argon 2ID Encryption
- Go Chi V5 Router
- HTML and HTMX
- Bootstrap V5 For css and components
- PostgreSQL for panel db
- fsouza/go-dockerclient to use docker!
- gorilla/websocket
- log/slog for logs
- tview for TUI on the setups

#### What's on the way

- Categories with multiple new config files you can create and import that dictate how servers are created 
- Port pool
- Add UDP ports too
- Server deletion
- sftp and backups
- Node management ( dont allow creating servers if the total allocated disk on a node is reaching the limit or dont allow starting a server if there wont be enough free ram to allocate etc )
- Server console + commands
- Per server local dbs ???

If you have a suggestion make an issue and explain your feature!!

### How to run:

##### Setup configs ( **IMPORTANT** )

1. On each node and the panel git clone the repo using `git clone https://github.com/JustSlowww/Aerocrest.git`
2. Then, open the appropriate subdir for your service inside the Setup directory
3. using go, run `go build <servicename>_setup.go` and then run `sudo ./<servicename>_setup`
4. Follow the installation wizard and make sure to use ports that are free ( i will add port checks soon tho ! )
   
##### On the nodes:

1. Git clone the repository
`git clone https://github.com/JustSlowww/Aerocrest.git`
2. then make sure go 1.27.1 is installed and run the daemon
`go run ./cmd`
3. Open the daemon's port on the firewall\
`sudo apt install ufw`\
`sudo ufw default deny incoming && sudo ufw default allow outgoing`\
`sudo ufw allow 22/tcp && sudo ufw allow <daemon_port>/tcp`\
`sudo ufw enable`\
or ideally setup nginx
    
##### On the panel:

1. Git clone the repository
`git clone https://github.com/JustSlowww/Aerocrest.git`
2. then make sure go 1.27.1 is installed and run the panel
`go run ./cmd`
3. Open the panel's port on the firewall\
  `sudo apt install ufw`\
  `sudo ufw default deny incoming && sudo ufw default allow outgoing`\
  `sudo ufw allow 22/tcp && sudo ufw allow <panel_port>/tcp`\
  `sudo ufw enable`\
   or ideally setup nginx
      
### Screenshots
| Login | Home Page |
| --- | --- |
| <img width="1679" height="992" alt="image" src="https://github.com/user-attachments/assets/96ad0c7e-a5ea-4469-b722-974ef3b941a6" /> | <img width="1679" height="1000" alt="image" src="https://github.com/user-attachments/assets/45a3763e-0035-4547-8adf-87023fdf7388" /> |
| Servers Page | Admin Panel |
| <img width="1676" height="991" alt="image" src="https://github.com/user-attachments/assets/4619ad58-8eaa-41b2-b125-e244f661c543" /> | <img width="1680" height="996" alt="image" src="https://github.com/user-attachments/assets/f04b6286-6c8c-49f6-96c6-87c55fc8e628" /> |

