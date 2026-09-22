import QtQuick

// Compact status for a bar: "VAULT 7.2 TB / 20 TB", "VAULT not set up" or
// "VAULT off". Polls the local API once a minute and never starts Vault; the
// host decides what a click does (e.g. run `vaultctl open`).
Item {
    id: root
    property string apiUrl: "http://127.0.0.1:8788/api/status"
    property int pollMs: 60000
    property var status: null
    property bool reachable: false
    signal clicked()

    readonly property VaultTheme theme: VaultTheme {}

    implicitWidth: label.implicitWidth + 16
    implicitHeight: 24

    function human(n) {
        if (!n) return "0 B"
        const u = ["B", "KB", "MB", "GB", "TB", "PB"]
        let i = 0
        while (n >= 1000 && i < u.length - 1) { n /= 1000; i++ }
        return (n >= 100 ? n.toFixed(0) : n.toFixed(1)) + " " + u[i]
    }

    function refresh() {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function() {
            if (xhr.readyState !== XMLHttpRequest.DONE) return
            root.reachable = xhr.status === 200
            if (root.reachable) {
                try { root.status = JSON.parse(xhr.responseText) } catch (e) { root.reachable = false }
            }
        }
        xhr.open("GET", root.apiUrl)
        xhr.send()
    }

    readonly property string text: {
        if (!reachable) return "VAULT off"
        if (!status || !status.setup_complete) return "VAULT not set up"
        return "VAULT " + human(status.storage.used_bytes) + " / " + human(status.storage.total_bytes)
    }
    readonly property bool hasHealthIssue: reachable && status && status.drives
        && (status.drives.warning > 0 || status.drives.critical > 0)

    Text {
        id: label
        anchors.centerIn: parent
        text: root.text + (root.hasHealthIssue ? "  ▲" : "")
        color: !root.reachable ? root.theme.inkMuted : root.hasHealthIssue ? root.theme.warn : root.theme.ink
        font.family: root.theme.font
        font.pixelSize: 12
    }

    MouseArea { anchors.fill: parent; onClicked: root.clicked() }
    Timer { interval: root.pollMs; running: true; repeat: true; triggeredOnStart: true; onTriggered: root.refresh() }
}
