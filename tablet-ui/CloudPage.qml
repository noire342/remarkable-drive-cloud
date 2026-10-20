// SPDX-License-Identifier: AGPL-3.0-only
import QtQuick
import QtQuick.Layouts
import ark.controls as ArkControls
import "Config.js" as Config

Item {
    id: page
    property bool probe: false
    property var cloudState: { return {}; }
    property var history: []
    property var selectedSnapshot: null
    property string message: ""
    property bool busy: false
    property bool connected: false

    function request(method, path, data, callback) {
        var xhr = new XMLHttpRequest();
        xhr.onreadystatechange = function() {
            if (xhr.readyState !== XMLHttpRequest.DONE) return;
            var result = null;
            try { result = JSON.parse(xhr.responseText); } catch (error) {}
            callback(xhr.status, result);
        };
        xhr.open(method, Config.endpoint + path);
        xhr.setRequestHeader("Authorization", "Bearer " + Config.token);
        xhr.setRequestHeader("Content-Type", "application/json");
        xhr.send(data === null ? "" : JSON.stringify(data));
    }
    function refresh() {
        if (!visible || probe || busy) return;
        request("GET", "/v1/state", null, function(status, result) {
            connected = status === 200 && !!result;
            if (connected && JSON.stringify(result) !== JSON.stringify(cloudState)) cloudState = result;
        });
    }
    function action(path, data) {
        if (busy) return;
        busy = true; message = "";
        request("POST", path, data, function(status, result) {
            busy = false;
            message = status >= 200 && status < 300 ? "Operazione avviata." : (result && result.error ? result.error : "Operazione non disponibile. Riprova.");
            refresh();
        });
    }
    function showHistory() {
        busy = true;
        request("GET", "/v1/history", null, function(status, result) {
            busy = false;
            if (status === 200) history = result;
            else message = result && result.error ? result.error : "Cronologia non disponibile.";
        });
    }
    function dateLabel(value) {
        if (!value || value.indexOf("0001-") === 0) return "Ancora nessun salvataggio verificato";
        return new Date(value).toLocaleString(Qt.locale("it_IT"), "dd/MM/yyyy HH:mm");
    }
    Component.onCompleted: {
        request("POST", "/v1/ui-ready", {nonce: Config.nonce}, function(status, result) {});
        refresh();
    }
    onVisibleChanged: { if (visible) refresh(); }
    Timer { interval: 10000; repeat: true; running: page.visible && !page.probe; onTriggered: page.refresh() }
    ArkControls.PlaceholderBar { id: placeholder; width: parent.width }
    Flickable {
        anchors { top: placeholder.bottom; bottom: parent.bottom; left: parent.left; right: parent.right }
        contentWidth: width
        contentHeight: content.implicitHeight + 120
        clip: true
        flickableDirection: Flickable.VerticalFlick
        Column {
            id: content
            x: 64; y: 56; width: parent.width - 128; spacing: 30
            ArkControls.Title { type: ArkControls.Title.Large; text: "Cloud"; width: parent.width }
            Text { width: parent.width; text: "Google Drive"; font.pixelSize: 32; color: "black"; textFormat: Text.PlainText }
            Text {
                width: parent.width; font.pixelSize: 27; wrapMode: Text.Wrap; color: "black"; textFormat: Text.PlainText
                text: !page.connected ? "Servizio non disponibile." : !page.cloudState.linked ? "Collega il tuo account Google dal programma di installazione sul computer." : (page.cloudState.account || "Account collegato")
            }
            Text {
                width: parent.width; font.pixelSize: 25; wrapMode: Text.Wrap; color: "#555555"; textFormat: Text.PlainText
                text: page.cloudState.active ? "Documenti sincronizzati direttamente con Google Drive." : "Migrazione non attiva. Il cloud precedente resta invariato finché backup e ripristino non sono verificati."
            }
            Text {
                width: parent.width; visible: page.cloudState.linked === true
                text: "Ultimo salvataggio su Drive: " + page.dateLabel(page.cloudState.last_sync) + "\nOperazioni pendenti: " + (page.cloudState.pending || 0) + (page.cloudState.paused ? "\nSincronizzazione in pausa" : "")
                font.pixelSize: 25; wrapMode: Text.Wrap; color: "black"; textFormat: Text.PlainText
            }
            Text { width: parent.width; visible: !!page.cloudState.quota && !!page.cloudState.quota.limit; text: page.cloudState.quota ? "Spazio Drive: " + (Number(page.cloudState.quota.usage || 0) / 1000000000).toFixed(2) + " GB di " + (Number(page.cloudState.quota.limit || 0) / 1000000000).toFixed(2) + " GB" : ""; font.pixelSize: 25; wrapMode: Text.Wrap; textFormat: Text.PlainText }
            CloudButton { text: "Sincronizza ora"; enabled: page.connected && page.cloudState.ready && page.cloudState.active && !page.busy; onClicked: page.action("/v1/sync", {}) }
            CloudButton { text: page.cloudState.paused ? "Riprendi sincronizzazione" : "Metti in pausa"; enabled: page.connected && page.cloudState.ready && !page.busy; onClicked: page.action("/v1/pause", {paused: !page.cloudState.paused}) }
            CloudButton { text: "Cronologia e ripristino"; enabled: page.connected && page.cloudState.ready && !page.busy; onClicked: page.showHistory() }
            Text { width: parent.width; visible: !!page.message; text: page.message; font.pixelSize: 25; wrapMode: Text.Wrap; color: "black"; textFormat: Text.PlainText }
            Text { width: parent.width; visible: !!page.cloudState.last_error; text: page.cloudState.last_error || ""; font.pixelSize: 25; wrapMode: Text.Wrap; color: "black"; textFormat: Text.PlainText }
            Text { width: parent.width; text: "Codice sorgente e licenza AGPL-3.0:\ngithub.com/noire342/remarkable-drive-cloud"; font.pixelSize: 22; wrapMode: Text.Wrap; color: "#555555"; textFormat: Text.PlainText }
            Repeater {
                model: page.history
                CloudButton {
                    required property var modelData
                    text: page.dateLabel(modelData.created) + " · " + Object.keys(modelData.documents).length + " documenti"
                    onClicked: page.selectedSnapshot = modelData
                }
            }
            Text { visible: page.selectedSnapshot !== null; width: parent.width; text: "Il ripristino crea una nuova copia e conserva il documento attuale."; font.pixelSize: 25; wrapMode: Text.Wrap; textFormat: Text.PlainText }
            Repeater {
                model: page.selectedSnapshot ? Object.keys(page.selectedSnapshot.documents) : []
                CloudButton {
                    required property string modelData
                    text: "Ripristina: " + (page.selectedSnapshot.names && page.selectedSnapshot.names[modelData] ? page.selectedSnapshot.names[modelData] : "Documento")
                    enabled: !page.busy
                    onClicked: page.action("/v1/restore", {snapshot_id: page.selectedSnapshot.snapshot_id, document_id: modelData})
                }
            }
        }
    }
}
