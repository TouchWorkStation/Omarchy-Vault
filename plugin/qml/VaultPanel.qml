import QtQuick
import QtQuick.Layouts

// Quick-action panel. It never runs commands itself: it emits
// actionRequested(id) and the host maps ids to the commands in manifest.json.
Rectangle {
    id: panel
    signal actionRequested(string id)
    readonly property VaultTheme theme: VaultTheme {}

    width: 320
    implicitHeight: col.implicitHeight + 32
    color: theme.surface
    border.color: theme.border
    radius: 6

    ColumnLayout {
        id: col
        anchors.fill: parent
        anchors.margins: 16
        spacing: 12

        Text { text: "VAULT"; color: panel.theme.inkStrong; font.family: panel.theme.font; font.pixelSize: 14; font.bold: true; font.letterSpacing: 2 }

        VaultStatusWidget { Layout.fillWidth: true }

        Repeater {
            model: [
                { id: "power", label: "TURN ON / OFF", detail: "Vault only runs when you turn it on" },
                { id: "upload", label: "UPLOAD", detail: "Phone → Vault  ·  Super+Shift+U" },
                { id: "download", label: "DOWNLOAD", detail: "Vault → Phone  ·  Super+Shift+D" },
                { id: "files", label: "OPEN FILES", detail: "Browse your Vault" },
                { id: "storage", label: "STORAGE", detail: "Drives and health" }
            ]
            delegate: Rectangle {
                required property var modelData
                Layout.fillWidth: true
                implicitHeight: 48
                radius: 6
                color: area.containsMouse ? "#181b1b" : "transparent"
                border.color: area.containsMouse ? panel.theme.accent : panel.theme.border

                Column {
                    anchors.verticalCenter: parent.verticalCenter
                    anchors.left: parent.left
                    anchors.leftMargin: 12
                    Text { text: modelData.label; color: panel.theme.inkStrong; font.family: panel.theme.font; font.pixelSize: 12 }
                    Text { text: modelData.detail; color: panel.theme.inkMuted; font.family: panel.theme.font; font.pixelSize: 11 }
                }
                MouseArea { id: area; anchors.fill: parent; hoverEnabled: true; onClicked: panel.actionRequested(modelData.id) }
            }
        }
    }
}
