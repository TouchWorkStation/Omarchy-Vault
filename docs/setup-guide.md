# Omarchy Vault setup guide

This guide takes you from a spare drive to a working Vault with your own account, family accounts, a file browser, phone transfers by QR code in both directions, and share links. Follow the parts in order the first time.

1. [What you need](#1-what-you-need)
2. [Prepare your drive](#2-prepare-your-drive)
3. [Install Vault](#3-install-vault)
4. [First-run setup](#4-first-run-setup)
5. [Files](#5-files)
6. [Users](#6-users)
7. [Keyboard shortcuts](#7-keyboard-shortcuts)
8. [Upload from your phone](#8-upload-from-your-phone)
9. [Download to your phone and share links](#9-download-to-your-phone-and-share-links)
10. [Everyday use](#10-everyday-use)
11. [Troubleshooting](#11-troubleshooting)
12. [Uninstall](#12-uninstall)

> **Vault never formats, partitions, erases or mounts drives.** It only uses a drive that is already mounted. Section 2 shows the few one-time commands *you* run to get a drive ready. Read each one before you run it.

---

## 1. What you need

- A computer running Omarchy (or Arch Linux).
- A second drive for your files: internal SATA/NVMe, or USB. Vault will never use the drive Omarchy runs from.
- About 10 minutes, plus a few minutes for the first build.

Commands below are typed in a terminal (Super + Enter in Omarchy).

---

## 2. Prepare your drive

### 2.1 Find your drives

```sh
lsblk -o NAME,SIZE,MODEL,FSTYPE,LABEL,MOUNTPOINTS
```

Example:

```
NAME          SIZE MODEL                 FSTYPE      LABEL  MOUNTPOINTS
nvme0n1     476.9G Samsung SSD 980 PRO
├─nvme0n1p1     2G                       vfat               /boot
└─nvme0n1p2 474.9G                       crypto_LUKS
  └─root    474.9G                       btrfs              /home, /
sda           7.3T WDC WD80EFZZ
└─sda1        7.3T                       ext4        wdred
```

- The drive mounted at `/`, `/boot` or `/home` (here `nvme0n1`) is your **system drive**. Never touch it.
- The other drive (here `sda`, partition `sda1`) is the one for your Vault.

If Vault is already installed, `vaultctl disks` shows the same list and marks the system drive **SYSTEM · PROTECTED**.

Then choose the section that matches your drive:

| Your drive | Go to |
|---|---|
| Already has a filesystem (ext4, xfs, btrfs, exFAT, NTFS), possibly with files | [2.2 Mount it permanently](#22-mount-it-permanently-recommended) |
| Brand new or blank (`FSTYPE` is empty, no partitions) | [2.4 Prepare a blank drive](#24-prepare-a-blank-drive-erases-it) |
| Encrypted (`crypto_LUKS`) | [2.5 Encrypted drives](#25-encrypted-drives) |

### 2.2 Mount it permanently (recommended)

A permanent mount means the drive is always at the same folder, so Vault finds it whenever you turn Vault on.

**1. Get the drive's UUID.** Replace `sda1` with your partition:

```sh
lsblk -no UUID,FSTYPE /dev/sda1
```

```
3f2b7c1e-5d8a-4e61-9c0f-1a2b3c4d5e6f ext4
```

**2. Create the mount folder:**

```sh
sudo mkdir -p /mnt/vault-disk1
```

**3. Add one line to `/etc/fstab`.** Open it:

```sh
sudo nano /etc/fstab
```

Add a line at the end. Use your UUID, and pick the line for your filesystem.

ext4, xfs or btrfs:

```
UUID=3f2b7c1e-5d8a-4e61-9c0f-1a2b3c4d5e6f  /mnt/vault-disk1  ext4  defaults,nofail,x-systemd.device-timeout=10s  0 2
```

(Change `ext4` to `xfs` or `btrfs` if that is your filesystem.)

exFAT or NTFS. These don't store Linux owners, so the options make the files yours. Check your IDs with `id -u` and `id -g` (usually 1000):

```
UUID=ABCD-1234  /mnt/vault-disk1  exfat  defaults,nofail,uid=1000,gid=1000,umask=077,x-systemd.device-timeout=10s  0 0
UUID=0123ABCD4567EF89  /mnt/vault-disk1  ntfs3  defaults,nofail,uid=1000,gid=1000,umask=077,x-systemd.device-timeout=10s  0 0
```

What the options mean:

- `nofail`: the computer still boots if the drive is unplugged.
- `x-systemd.device-timeout=10s`: don't wait long for a missing drive.

Save with Ctrl+O, Enter, then exit with Ctrl+X.

**4. Mount it and check:**

```sh
sudo systemctl daemon-reload
sudo mount -a
findmnt /mnt/vault-disk1
```

`findmnt` should print one line showing your drive. If `mount -a` prints an error, open `/etc/fstab` again and fix the typo. Don't reboot with a broken line.

**5. Make it yours** (ext4, xfs and btrfs only; exFAT and NTFS were handled by `uid=` above):

```sh
sudo chown "$USER:$USER" /mnt/vault-disk1
touch /mnt/vault-disk1/.vault-write-test && rm /mnt/vault-disk1/.vault-write-test && echo "writable"
```

This only changes the owner of the top folder. Existing files keep their owners. If you have existing files you want to manage through Vault, also run `sudo chown -R "$USER:$USER" /mnt/vault-disk1/<folder>` for those folders.

### 2.3 Quick alternative: mount from the file manager

Open Files (Nautilus) and click the drive in the sidebar. It is mounted at `/run/media/<you>/<label>` and Vault can use it right away.

The downside: it is only mounted while you are logged in, and only after you click it. For an always-on Vault, use 2.2.

### 2.4 Prepare a blank drive (erases it)

> **Warning:** these commands erase the drive you name. Vault never runs them for you. Triple-check the device name. It must be the new drive, never your system drive. If in doubt, stop.

**1. Identify the new drive by size and model.** It should have no partitions and no mount points. Unplug and replug a USB drive and run this again to be sure which one it is:

```sh
lsblk -o NAME,SIZE,MODEL,SERIAL,FSTYPE,MOUNTPOINTS
```

**2. Set a variable** so you type the name only once. Replace `sdX` with your drive, e.g. `sdb`:

```sh
DISK=/dev/sdX
lsblk "$DISK"          # look one last time: size and model must match the new drive
```

**3. Create one partition and an ext4 filesystem:**

```sh
sudo parted "$DISK" --script mklabel gpt mkpart vault ext4 0% 100%
sudo mkfs.ext4 -L vault "${DISK}1"        # NVMe drives: use "${DISK}p1"
```

**4. Mount it permanently** by following [2.2](#22-mount-it-permanently-recommended) with the new partition (`sdX1`).

### 2.5 Encrypted drives

If the drive is LUKS-encrypted (`crypto_LUKS`), it must be unlocked before it can be mounted. For an always-on Vault, unlock it at boot with a key file (`/etc/crypttab`), then mount the unlocked device (`/dev/mapper/<name>`) as in 2.2. See the Arch Wiki page "dm-crypt/System configuration". Vault never unlocks or changes encrypted volumes. It shows a locked drive as "not unlocked" and waits.

### 2.6 Check that Vault can see it

After installing (section 3):

```sh
vaultctl storage
```

```
Drives Vault can use:
  sda1       WDC WD80EFZZ-68BTXN0       /mnt/vault-disk1             5.3 TB free of 7.9 TB
```

If your drive is missing or not usable, see [Troubleshooting](#11-troubleshooting).

---

## 3. Install Vault

**The quick way.** Make sure your drive is mounted (section 2; clicking it in the Files app is enough), then:

```sh
sudo pacman -S --needed git && { git -C ~/Omarchy-Vault pull --ff-only 2>/dev/null || git clone https://github.com/TouchWorkStation/Omarchy-Vault.git ~/Omarchy-Vault; } && ~/Omarchy-Vault/scripts/install.sh --express
```

This does everything below plus sections 7 and 8's one-time steps (shortcuts, firewall rule for your home network) and opens the setup screen (section 4). Add `--with-files` to also build Files (section 5).

**Or step by step:**

```sh
sudo pacman -S --needed git go npm base-devel smartmontools
git clone https://github.com/TouchWorkStation/Omarchy-Vault.git ~/Omarchy-Vault
cd ~/Omarchy-Vault
./scripts/install.sh --dry-run     # shows every step, changes nothing
./scripts/install.sh
```

The installer explains each step and asks before anything optional:

| Step | What it does | Needs sudo? |
|---|---|---|
| Build | Builds Vault into `bin/` | no |
| File service | Builds SFTPGo v2.7.6 from its official source, pinned to one verified commit, into `~/.local/share/omarchy-vault/sftpgo` (a few minutes) | no |
| Config | Creates `~/.config/omarchy-vault` (private) | no |
| `/srv/vault` | Creates the shortcut `/srv/vault` → your Vault (asks) | yes, once |
| Programs | Installs `vaultd` and `vaultctl` to `~/.local/bin` | no |
| Service | Installs the `omarchy-vault` user service. It **never starts by itself**; the installer asks whether to turn it on now | no |
| Shortcuts | Checks Super+Alt+V/U/D for conflicts. Changes nothing (install them in section 7) | no |

If `vaultctl` is "not found", add `~/.local/bin` to your PATH:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc && source ~/.bashrc
```

Check everything:

```sh
vaultctl doctor
```

---

### Turning Vault on and off

Vault only runs when you turn it on. Nothing starts at login or boot, and nothing stays in the background after you turn it off.

It also **turns itself off** after 15 minutes with nothing to do: nobody using the dashboard, no QR code or share link active and nothing being transferred. The shortcuts turn it on again in a second or two. To change the time, set `"auto_off_minutes"` in `~/.config/omarchy-vault/config.json` (5 to 1440 minutes, or `0` for never), then `vaultctl off && vaultctl on`.

```sh
vaultctl on        # turn on (Files starts too, once your drive is ready)
vaultctl off       # turn off (stops Files too)
vaultctl status    # is it on?
```

Opening Vault turns it on for you: `vaultctl open`, `vaultctl setup`, or the Super+Alt+V shortcut. Admins can also click **Turn off Vault** in the dashboard sidebar.

## 4. First-run setup

```sh
vaultctl setup
```

Your browser opens, already signed in as this computer's owner. The five screens:

1. **Welcome.** Click Get Started.
2. **Storage.** Your drives. The system drive shows SYSTEM · Protected and can't be picked. Choose your Vault drive and click Select Drive.
3. **Vault Storage.** Choose "A Vault folder" (recommended: creates `Vault` on the drive and leaves your other files alone) or "The whole drive". Keep "Create folders" on for Photos, Documents, Backups, Projects, Phone Uploads and Shared. Click **Create Vault**.
4. **Account.** Create your admin account (username + password, 10+ characters). You'll use it to sign in to Vault and Files. You can set up two-factor later in Account.
5. **Your Vault is ready.** Shows your free space. **Open Files** takes you straight to your files.

### The same from the terminal

```sh
vaultctl storage                      # drives you can use
vaultctl storage use sda1             # or: vaultctl storage use /mnt/vault-disk1
vaultctl users add chris              # first account is always an admin; asks for a password
vaultctl link                         # optional: /srv/vault shortcut (asks for sudo once)
```

Options for `storage use`:

- `--folder Media/Vault`: a different folder on the drive
- `--whole-drive`: use the whole drive
- `--no-default-folders`: skip Photos, Documents, …

### Where your files are

```
/srv/vault/                      → ~/.local/share/omarchy-vault/current → /mnt/vault-disk1/Vault
  Photos/  Documents/  Backups/  Projects/  Phone Uploads/  Shared/
```

They are ordinary files on your drive. You can also open them with any app at `/srv/vault` or `/mnt/vault-disk1/Vault`.

---

## 5. Files

Files is your Vault in the browser: browse, upload, download (folders as zip), create, rename and move.

- Open it from the dashboard (**Open Files**), from the Files page, or at http://127.0.0.1:8788/files/web/client/files
- Signing in to Vault signs you in to Files as well. If Files asks you to sign in, use the same Vault username and password.
- **Admins** see every folder. **Family** sees only their folders (read & write, or read only per folder). **Guests** see only their folders, always read only.
- Uploads go straight to the drive. There is no size limit other than free space.

Vault's pages (and Files) are reachable **from this computer only**. Phones use the QR codes (sections 8 and 9) instead. Please don't open the dashboard to your network by changing `listen`.

If Files says it is not installed, run `./scripts/build-sftpgo.sh` (or re-run the installer), then `vaultctl off && vaultctl on`.

---

## 6. Users

Open **Users** in the dashboard (admins only), or use `vaultctl users`.

| Role | Can do |
|---|---|
| Admin | Everything: storage, users, settings, all files |
| Family | Open the folders you give them; read & write or read only per folder |
| Guest | Open the folders you give them, read only |

### In the dashboard

- **Create user:** username, password, role, then tick folders (and pick Read & write or Read only).
- **Folders:** change what they can open.
- **Reset password:** sets a new one and signs them out everywhere.
- **Disable / Enable:** a disabled user is signed out immediately and can't sign in.
- **Remove:** deletes the account. Their files stay in the Vault.

### In the terminal

```sh
vaultctl users                                                   # list
vaultctl users add ann --role family --folders "Photos,Documents:ro"
vaultctl users add gus --role guest --folders "Shared"
vaultctl users folders ann "Photos,Documents,Shared:ro"
vaultctl users reset-password ann
vaultctl users disable ann
vaultctl users enable ann
vaultctl users remove gus
```

### Your own account

Open **Account** (click your name in the sidebar):

- **Change password:** other devices are signed out.
- **Two-factor sign-in:** scan the QR code with an authenticator app (Aegis, 2FAS, Google Authenticator, 1Password…) and enter the 6-digit code. From then on, signing in asks for a code. To turn it off, enter your password.

### Rules that keep you safe

- Vault always keeps at least one active admin. You can't disable, demote or remove the last one.
- After 5 wrong passwords, sign-in for that account pauses for 1 minute, then 2, 4… up to 15 minutes.
- Passwords are stored only as argon2id hashes (in `~/.config/omarchy-vault/users.json`, private to you).

---

## 7. Keyboard shortcuts

Vault can add these, but only when you ask and only if they are free:

| Shortcut | Does |
|---|---|
| Super + Alt + V | Open Vault |
| Super + Alt + U | Upload to Vault (phone → Vault) |
| Super + Alt + D | Download from Vault (Vault → phone) |

```sh
vaultctl shortcuts             # check: which are free, which are taken and by what
vaultctl shortcuts install     # shows exactly what it will write, then asks
```

Prefer other keys? Open **Settings → Keyboard shortcuts** in the dashboard: pick modifiers and a key for each action, see straight away whether it's free, and save. A shortcut that's already used (by Omarchy, Beam or you) is **never replaced**. To use Vault's suggested free alternative instead (e.g. Super + Alt + U), run `vaultctl shortcuts install --use-suggestions`. To undo everything: `vaultctl shortcuts remove`. Details: [shortcuts.md](shortcuts.md).

---

## 8. Upload from your phone

Send photos, videos and files from your phone into the Vault, with no app and no account on the phone.

**Before the first time**

1. Your phone must be on the **same Wi-Fi** as this computer (or the same home network, if the computer is on Ethernet).
2. Omarchy's firewall (ufw) blocks incoming connections by default. Allow Vault's phone port from your home network only, once:

   ```sh
   sudo ufw status                                   # "inactive"? nothing to do
   sudo ufw allow from 192.168.1.0/24 to any port 8790 proto tcp
   ```

   Replace `192.168.1.0/24` with your network: run `ip -4 route | grep -v default` and use the address before `dev` on your Wi-Fi/Ethernet line (e.g. `192.168.0.0/24` or `10.0.0.0/24`). Vault listens on this port **only while an upload code is showing**. To undo: `sudo ufw delete allow from 192.168.1.0/24 to any port 8790 proto tcp`.

**Every time**

1. Press **Super + Alt + U** (or run `vaultctl upload`, or click **Upload** in the dashboard). Vault turns on if it was off and shows a QR code.
2. Point your phone's camera at the code and tap the link.
3. Choose **Select Photos**, **Take Photo**, **Select Videos** or **Choose Files**. You see progress for each file, then a list of what arrived.
4. The files appear on the computer's screen as they arrive, and in **Vault → Phone Uploads** (`/srv/vault/Phone Uploads`).
5. Click **Stop** when you're done, or just leave it: the code stops working after 10 minutes.

In a terminal only? `vaultctl upload --terminal` prints the QR code in the terminal and stops the code when you press Ctrl+C. Other options: `--folder Photos` to send into another Vault folder, `--minutes 30` for a longer code (up to 60).

**Good to know**

- Nothing is ever overwritten: a second `IMG_0001.jpg` becomes `IMG_0001 (1).jpg`.
- Vault stops accepting files before your drive is completely full (it keeps 1 GB free).
- Family members can create codes for folders they can write; guests can't.
- Each code works for one folder, can't be used to see or download anything, and ends when it expires, when you click Stop, or after 1000 files.
- Use it on your own Wi-Fi, not on public Wi-Fi: on the local network the upload is not encrypted.
- Phone not connecting? See Troubleshooting.

---

## 9. Download to your phone and share links

The phone setup from section 8 (same Wi-Fi, firewall rule for port 8790) applies here too.

**Send a file or folder to your phone**

1. Press **Super + Alt + D** (or click **Download** in the dashboard). Vault turns on if it was off and shows your Vault's folders.
2. Click a file or folder to choose it (**Open ›** goes into a folder). A folder arrives on the phone as one `.zip` file.
3. Keep **To my phone**, choose how long the code works (10 minutes by default) and how many phones may use it (once by default), then **Show QR code**.
4. Scan it with the phone's camera and tap **Download**. The screen on the computer says *Done* when it has been downloaded.

From a terminal: `vaultctl download "/srv/vault/Photos/2024/beach.jpg"` (or the Vault path, `Photos/2024/beach.jpg`), with `--terminal` to print the QR code in the terminal, `--minutes 30`, `--downloads 3`.

An interrupted download can be retried from the same phone until the code expires; it isn't counted twice.

**Share links**

For sending something to someone else on the same Wi-Fi (a family member's phone or laptop):

1. In **Download**, choose the file or folder, then **Share link**.
2. Pick how long it works (10 minutes, 1 hour, 4 hours or 24 hours), how many downloads (one, up to 5, up to 25, or unlimited until it expires) and, if you like, a password.
3. **Create share link** shows a QR code and the link with a **Copy** button.

Share links are read only: people can download, never change or delete. A shared folder shows its files one by one and as **Download all (.zip)**. All your active links are listed under **Download → Share links**, with **Stop** to end one immediately. From a terminal: `vaultctl share Photos/2024 --expires 4h --downloads 5 --password`.

Who can do what: admins everything; family members can send and share from folders they can open; guests can send to their own phone but can't create share links.

While a share link is active Vault stays on and keeps its phone port open (only for that link's page), so stop shares you no longer need. Links never work from outside your home network.

---

## 10. Everyday use

- **Turn on / off:** `vaultctl on` / `vaultctl off`. Vault never runs unless you turn it on.
- **Open Vault:** `vaultctl open` (turns it on if needed), or Super + Alt + V once you've installed the shortcuts (section 7).
- **Phone → Vault:** Super + Alt + U or `vaultctl upload` (section 8).
- **Vault → Phone, share links:** Super + Alt + D or `vaultctl download` / `vaultctl share` (section 9).
- **Status at a glance:** `vaultctl status`, or the Home page.
- **Drive unplugged or not mounted (while Vault is on):** Vault shows your storage as *Offline*, pauses Files, and never writes anything to your system drive in the meantime. Plug the drive back in (or `sudo mount -a`) and everything resumes within about 20 seconds.
- **Drive mounted somewhere else:** Vault shows *Drive moved*. Choose it again in Storage → *Use it at its new location*.
- **Change drives:** Storage → Change drive. Files on the old drive stay there. Copy them over if you want them in the new Vault.
- **Stop using a drive:** Storage → Stop using this drive (or `vaultctl storage forget`). Nothing is deleted.
- **Back up Vault's own settings:** copy `~/.config/omarchy-vault/` (settings, users, secrets; keep it private) and `~/.local/share/omarchy-vault/sftpgo/data/`.

---

## 11. Troubleshooting

Start with:

```sh
vaultctl doctor
vaultctl logs -f        # live log of the Vault service
```

| Problem | Fix |
|---|---|
| `vaultctl on` fails | Check `vaultctl logs`; re-run `./scripts/install.sh` if the service is missing |
| My drive is not listed under "Drives Vault can use" | It isn't mounted, or it's mounted somewhere Vault avoids (`/var`, `/usr`, `/tmp`, `/root`…). Mount it under `/mnt/…` (section 2.2) |
| Drive shows **Not mounted** | Mount it (2.2 or 2.3). Vault never mounts drives itself |
| Drive shows **Can't use: No filesystem** | It's blank. See 2.4 (erases it) |
| Drive shows **Encrypted volume not unlocked** | Unlock it first (2.5) |
| Drive shows **Read-only** | Check the filesystem (`sudo dmesg \| tail`) or the mount options in `/etc/fstab` |
| "Permission denied" when uploading | The drive folder isn't owned by you: `sudo chown "$USER:$USER" /mnt/vault-disk1/Vault` (ext4/xfs/btrfs), or add `uid=`/`gid=` for exFAT/NTFS (2.2) |
| Files says "not installed" | `./scripts/build-sftpgo.sh`, then `vaultctl off && vaultctl on` |
| Files says "waiting for storage" | Your drive is offline; see "Drive unplugged" above |
| Forgot the admin password | On this computer: `vaultctl users reset-password <name>` (the terminal is trusted as the owner) |
| Locked out after wrong passwords | Wait 1–15 minutes, or reset the password from the terminal |
| "That sign-in link expired" | Open Vault again with `vaultctl open` (links from it work once, for 30 seconds) |
| "Vault is off" | That's the default, and Vault turns itself off after 15 idle minutes. Turn it on with `vaultctl on`, a shortcut, or `vaultctl open` |
| Phone says "can't connect" / page never loads | Phone on the same Wi-Fi (not mobile data, not a guest network)? Firewall rule from section 8 added? Some routers isolate Wi-Fi devices ("AP/client isolation"); turn that off for your home network |
| "Your phone can't reach this computer: not connected to a local network" | The computer has no private network address (e.g. only a VPN). Connect to your home Wi-Fi/Ethernet, or set `"transfer": {"host": "<your LAN IP>"}` in `~/.config/omarchy-vault/config.json` |
| "port 8790 is in use by another program" | Set another port: `"transfer": {"port": 8791}` in config.json (and allow it in ufw) |
| "LINK ENDED" on the phone | The code expired, was stopped or was used up. Make a new one (Super + Alt + U or D) |
| Share link asks for a password you don't have | Ask the person who shared it. After several wrong tries it waits 1–15 minutes |
| "That folder has too many files" | Folder links hold up to 20 000 files; share a smaller folder |
| A second phone can't download | The code was for one download. Choose more downloads, or make a new code |
| Could not identify the system drive | Vault then refuses every drive, to be safe. Run `vaultctl disks` and `findmnt /`, and report it as an issue |

---

## 12. Uninstall

```sh
cd ~/Omarchy-Vault
./scripts/uninstall.sh --dry-run
./scripts/uninstall.sh
```

This removes Vault's keyboard shortcuts, the programs, the service, the file service program and the `/srv/vault` shortcut. It keeps your settings, your users and **every file on your drive**. To also remove settings: `rm -rf ~/.config/omarchy-vault ~/.local/share/omarchy-vault`. Your fstab line and drive are untouched; remove the fstab line yourself if you no longer want the drive mounted.
