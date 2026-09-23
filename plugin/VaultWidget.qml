import QtQuick
import Quickshell.Io
import qs.Commons
import qs.Ui

// Omarchy Vault bar widget: one icon in the bar.
//   click         open Vault (turns it on; it turns itself off when idle)
//   right-click   Upload to Vault: QR code for your phone
//   middle-click  Download from Vault: sends the files you copied
// Before Vault is installed, a click opens a terminal that runs the
// installer from this plugin's own folder, so every step is visible.
// The widget itself runs nothing in the background.
BarWidget {
  id: root
  moduleName: "touchworkstation.vault"

  property bool installed: false
  readonly property string vaultctl: "$HOME/.local/bin/vaultctl"
  // This file lives in <plugin>/plugin/, so the plugin folder is one up.
  readonly property string pluginDir: decodeURIComponent(String(Qt.resolvedUrl("..")).replace(/^file:\/\//, "").replace(/\/$/, ""))

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  function quote(s) {
    return "'" + String(s).replace(/'/g, "'\\''") + "'"
  }

  function installInTerminal() {
    var inner = quote(pluginDir + "/scripts/install.sh") + " --express; echo; read -rp 'Press Enter to close this window.'"
    root.bar.run("xdg-terminal-exec bash -c " + quote(inner))
  }

  function act(button) {
    if (!root.bar) return
    if (!root.installed) {
      installInTerminal()
      return
    }
    if (button === Qt.RightButton) root.bar.run(vaultctl + " upload")
    else if (button === Qt.MiddleButton) root.bar.run(vaultctl + " download")
    else root.bar.run(vaultctl + " open")
  }

  // Is Vault installed? Checked when the widget loads and after each click
  // (the installer may have just finished). A single `test`, nothing more.
  Process {
    id: probe
    command: ["sh", "-c", "test -x \"$HOME/.local/bin/vaultctl\""]
    onExited: function(exitCode, exitStatus) { root.installed = exitCode === 0 }
  }

  Component.onCompleted: probe.running = true

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: ""
    slotSize: Style.bar.statusSlot
    dimmed: !root.installed
    tooltipText: root.installed
      ? "Vault\nClick: open · Right-click: upload from phone · Middle-click: send copied files to phone"
      : "Vault is not installed yet. Click to install it (opens a terminal)."

    onPressed: function(b) {
      root.act(b)
      probe.running = true
    }
  }
}
