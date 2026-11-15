#!/bin/bash

CONFIG_DIR="/etc/aerocrest"
CONFIG_FILE="$CONFIG_DIR/aerocrest.cfg"

mkdir -p "$CONFIG_DIR"

read -r -p "Enter the port you want the service to run on: " port
if grep -q "^port=" /etc/aerocrest/aerocrest.cfg 2>/dev/null; then
    sudo sed -i "s/^port=.*/port=$port/" /etc/aerocrest/aerocrest.cfg
else
    printf "port=%s\n" "$port" | sudo tee -a /etc/aerocrest/aerocrest.cfg > /dev/null
fi

sudo apt update
sudo apt-get install -y docker.io docker-compose-v2
sudo systemctl enable --now docker

if [ -n "$SUDO_USER" ]; then
  sudo chown root:"$SUDO_USER" "$CONFIG_FILE"
fi
sudo chmod 770 "$CONFIG_FILE"
sudo chmod 660 "$CONFIG_FILE"