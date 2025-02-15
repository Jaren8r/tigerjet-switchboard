# TigerJet Switchboard
Use your MagicJack adapter with other VOIP applications

# Requirements
- All Platforms: [minimodem](https://github.com/kamalmostafa/minimodem)
- Linux: libasound2-dev (Debian-based) / alsa-lib-devel (RedHat-based)

# Usage
1. Copy [config-example.yml](./config-example.yml) to config.yml and edit it in your text editor of choice. 
2. Start tigerjet-switchboard
3. Setup [Clients](#clients)

# Clients
Discord:
 - [BetterDiscord](https://gist.github.com/Jaren8r/2d9b632e2a8db15cb0dbc5739d38686a)
 - [Moonlight](https://github.com/Jaren8r/tigerjet-switchboard-client-moonlight)

[Google Voice](https://github.com/Jaren8r/tigerjet-switchboard-client-gvoice)

# Troubleshooting

## `panic: Failed to open a device with path '/dev/hidraw*': Permission denied`

Use chown and chmod to give the users in the sudo group access to the device:
```sh
sudo chown root:sudo /dev/hidraw*
sudo chmod 770 /dev/hidraw*
```

## Caller ID not working
- Make sure your output volume isn't set too loud. On Mac OS, 70% is recommended, otherwise it distorts.
- Make sure caller-id in your config.yml is set to a valid value (`before-first-ring` or `after-first-ring`) and that your phone supports it.