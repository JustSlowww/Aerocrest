#!/bin/bash

CONFIG_DIR="/etc/aerocrest"
CONFIG_FILE="$CONFIG_DIR/aerocrest.cfg"

mkdir -p "$CONFIG_DIR"

# Web server port
read -r -p "Enter the port you want the service to run on: " port
if grep -q "^port=" /etc/aerocrest/aerocrest.cfg 2>/dev/null; then
    sudo sed -i "s/^port=.*/port=$port/" /etc/aerocrest/aerocrest.cfg
else
    printf "port=%s\n" "$port" | sudo tee -a /etc/aerocrest/aerocrest.cfg > /dev/null
fi

# DbPort
read -r -p "Enter the new db port: " dbPort
if grep -q "^dbPort=" /etc/aerocrest/aerocrest.cfg 2>/dev/null; then
    sudo sed -i "s/^dbPort=.*/dbPort=$dbPort/" /etc/aerocrest/aerocrest.cfg
else
    printf "dbPort=%s\n" "$dbPort" | sudo tee -a /etc/aerocrest/aerocrest.cfg > /dev/null
fi

# DbName
read -r -p "Enter the new db name: " dbName
if grep -q "^dbName=" /etc/aerocrest/aerocrest.cfg 2>/dev/null; then
    sudo sed -i "s/^dbName=.*/dbName=$dbName/" /etc/aerocrest/aerocrest.cfg
else
    printf "dbName=%s\n" "$dbName" | sudo tee -a /etc/aerocrest/aerocrest.cfg > /dev/null
fi


# DbPass
read -r -p "Enter the new db password: " dbPass
if grep -q "^dbPass=" /etc/aerocrest/aerocrest.cfg 2>/dev/null; then
    sudo sed -i "s/^dbPass=.*/dbPass=$dbPass/" /etc/aerocrest/aerocrest.cfg
else
    printf "dbPass=%s\n" "$dbPass" | sudo tee -a /etc/aerocrest/aerocrest.cfg > /dev/null
fi
sudo apt update
sudo apt-get install -y docker.io docker-compose-v2
sudo systemctl enable --now docker

if [ -n "$SUDO_USER" ]; then
  sudo chown root:"$SUDO_USER" "$CONFIG_FILE"
fi
sudo chmod 770 "$CONFIG_FILE"

docker run --name "$dbName" \
-e POSTGRES_PASSWORD="$dbPass" \
-e POSTGRES_DB="$dbName" \
-p 127.0.0.1:"$dbPort":5432 \
-v "$dbName"-data:/var/lib/postgresql \
--restart unless-stopped \
-d postgres:18