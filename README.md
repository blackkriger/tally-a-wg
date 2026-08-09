# tally(a)wg

`wg show` only counts bytes **since the interface last came up**, so the per-peer counters reset on every restart and reboot. **tally(a)wg** snapshots them on a timer, accumulates **reset-aware deltas** in a small JSON ledger, and shows per-peer **total / year / month / today** usage that survives restarts — in the terminal or the browser.

The `(a)` is optional: `tallywg` for WireGuard, `tallyawg` for AmneziaWG.

In the terminal (`tallyawg report`):

```
PEER            ADDRESS     TOTAL down   TOTAL up    YEAR 2026    MONTH 2026-06   TODAY
laptop          10.8.1.3    12.4 GiB     2.1 GiB     11.9 GiB     9.8 GiB         420.5 MiB
phone           10.8.1.2    830.0 MiB    44.0 MiB    780.0 MiB    512.0 MiB       12.0 MiB
```

The web page (`tallyawg serve`) shows totals plus live status — online / last handshake, current session, and endpoint:

![tally(a)wg web page](screenshots/tallyawg-web.png)

## Features

- Per-peer **total / year / month / today**, persistent across restarts and reboots. 
- **Reset-aware** — detects counter resets and keeps accumulating correctly.
- **Live view** — who's online, last handshake, current session, and endpoint.
- **Web page** with a light/dark theme, timezone offset, sortable columns, and month-by-month history. 
- Friendly peer names from `# name` comments in the server config, or a names file.
- A CLI report **and** a built-in web page — pick either or both.
- **Peer management** — add and remove peers from the page or the CLI: keys, an unused address and the client config with its QR are produced. Who may change things is decided by the tunnel address they come from. 
- **Self-updating** — the page offers new releases and installs them after checking their checksum.
- Single static binary, stdlib only. Works with both `wg` and `awg`.

## Usage

```sh
tallyawg            # report: per-peer total / year / month / today
tallyawg serve      # collector loop + web page (default 127.0.0.1:8082)
tallyawg collect    # take one snapshot (e.g. from cron)
```

Common flags: `-i <iface>`, `-config <server.conf>` (peer names), `-names <file>`, `-wg <binary>`, `-data <ledger.json>`, `-tz <zone>` (UTC, an offset like `+3`, or an IANA name); `serve` adds `-listen`, `-admin` and `-interval`. `report` also takes `-json`. `down` = peer download (server → peer), `up` = peer upload.

## Install

Reading the wg/awg counters needs **root**. Both ways install tally(a)wg as a service that survives reboots — systemd on Linux, launchd on macOS.

**From a release** — no clone or Go needed. The binary installs itself: it carries the service definition and the config template inside. 

```sh
# grab the linux archive for your arch from the latest release, then:
tar xzf tallyawg_linux_amd64.tar.gz
sudo ./tallyawg_linux_amd64 install
```

**From source** (needs Go 1.23+):

```sh
git clone https://github.com/blackkriger/tally-a-wg
cd tally-a-wg
make                 # or: make linux, for a static linux/amd64 binary
sudo ./tallyawg install
```

On macOS the `wg` / `awg` command-line tools have to be there too — `brew install wireguard-tools` covers `wg`.

Either way, `install` copies the binary to `/usr/local/bin/tallyawg`, sets up the service and (re)starts it. `sudo tallyawg uninstall` removes the service and the binary, keeping the ledger and your config.

### Setting flags

On **Linux** flags live in `/etc/tallyawg/tallyawg.env` (written on first install, yours is kept afterwards):

```sh
# edit TALLYAWG_FLAGS in /etc/tallyawg/tallyawg.env, then:
sudo systemctl restart tallyawg
sudo systemctl status tallyawg
```

On **macOS** they live in the launchd job, one `<string>` per word after `serve`:

```sh
# edit ProgramArguments in /Library/LaunchDaemons/com.github.blackkriger.tallyawg.plist, then:
sudo tallyawg install       # reloads the job
```

With no `-i`, every up wg/awg interface is auto-detected and read with the tool that supports it. Add `-config <server.conf>` to show friendly peer names.

Prefer to run it by hand, without a service? Just run the binary:

```sh
sudo ./tallyawg serve -config /etc/wireguard/wg0.conf
```

### Updating

The page checks for new releases and offers an **Update** button next to the version; pressing it swaps the binary and restarts the service. The same thing from the shell:

```sh
sudo tallyawg update
```

Downloads come from this repository's releases over HTTPS and are only installed after their SHA-256 matches the `SHA256SUMS` published with the release; anything older than what is running is refused.

## Managing peers

`tallyawg peer add <name>` generates a key pair, claims the lowest unused address in the interface's subnet, appends the peer to the server config, applies it to the running interface and writes a client config next to it — plus a QR image when `qrencode` is installed. Everything it needs (interface, config path, subnet, port, server key, AmneziaWG obfuscation parameters, endpoint) is read from the running setup, so there is nothing to configure:

```sh
sudo tallyawg peer add phone            # the only interface, or the AmneziaWG one
sudo tallyawg peer add phone -kind wg   # pick a flavour when both are up
sudo tallyawg peer rm phone             # also erases its history from the ledger
```

The same actions live behind the `⋯` menu on each row of the page, along with downloading the config and showing its QR.

### Who may change things

Adding a peer, deleting one, downloading a config or QR, installing an update — is allowed from two places only:

- **the loopback**, so an SSH tunnel works with no configuration at all;
- **the tunnel addresses you list in `-admin`**, e.g. `-admin 10.9.0.2` 

This leans on WireGuard itself: a packet arriving through the tunnel with source `10.9.0.2` can only have come from the peer whose `AllowedIPs` hold that address, because that is what cryptokey routing enforces. Nothing is stored in the browser and nothing can leak.

```sh
ssh -L 8082:127.0.0.1:8082 root@your-server   # no -admin needed
sudo tallyawg serve -admin 10.9.0.2           # or trust peer
```

Viewing stays open to everyone who can reach the page; only the actions are gated. 

**This is only as good as the firewall.** The address proves something because the page is reachable through the tunnel alone. Bind it wide without the `ufw` rules below and any host that can route to `10.9.0.2` may claim to be it, so keep both together. `X-Forwarded-For` is ignored on purpose, which also means the check cannot work behind a reverse proxy — put the proxy's own authentication in front if you need one.

### Webpage

Anyone who can open the page can **read** it, and it shows public keys, addresses, transfer volumes and peer endpoints — the current public IP of every client. Only changing things is gated, by address. That is why it binds to `127.0.0.1:8082` by default.

To reach it from your own devices, keep that default and forward the port over SSH:

```sh
ssh -L 8082:127.0.0.1:8082 root@your-server    # then open http://127.0.0.1:8082
```

If you would rather serve it to everyone already on the tunnel, bind wide **and** firewall the port to the wg/awg interfaces — binding alone puts it on the public internet:

```sh
sudo ufw allow in on wg0 to any port 8082 proto tcp
sudo ufw allow in on awg0 to any port 8082 proto tcp
sudo tallyawg serve -listen 0.0.0.0:8082 -admin 10.9.0.2
```

Add a rule for **every** interface you want to reach it from: a peer on an interface with no rule cannot open the page at all, whatever `-admin` says.

Putting it behind a reverse proxy with authentication works too.

## Building

Static assets are embedded via `go:embed`, so a plain Go build bundles the web page too — no Node.js toolchain. Requires Go 1.23+ 

```sh
make          # build ./tallyawg for the host
make dist     # cross-build every platform into dist/ + SHA256SUMS
```


## License

The MIT License (MIT). See [LICENSE](LICENSE) for details.
