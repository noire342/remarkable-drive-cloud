// SPDX-License-Identifier: AGPL-3.0-only
import QtQuick

Rectangle {
    id: button
    property string text: ""
    signal clicked()
    width: parent ? parent.width : 400
    height: label.implicitHeight + 40
    border.width: 2
    border.color: enabled ? "black" : "#999999"
    color: "white"
    radius: 8
    opacity: enabled ? 1 : 0.5
    Text {
        id: label
        anchors.centerIn: parent
        width: parent.width - 48
        text: button.text
        textFormat: Text.PlainText
        font.pixelSize: 27
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.Wrap
        color: "black"
    }
    MouseArea { anchors.fill: parent; enabled: button.enabled; onClicked: button.clicked() }
}
