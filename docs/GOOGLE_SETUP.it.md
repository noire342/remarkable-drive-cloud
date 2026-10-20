# Collegare il proprio Google Drive

Serve un client OAuth personale di tipo **Desktop**. La password Google viene
inserita soltanto sul sito Google, nel browser del computer. Il tablet usa un
refresh token e continua a funzionare senza il computer acceso.

**Ogni installazione usa il client OAuth del proprio utente.** Il progetto
distribuisce il software e la procedura di configurazione; il JSON del client e
i token dell'autore restano privati, esclusi dal repository e dai pacchetti.

1. Apri [Google Cloud Console](https://console.cloud.google.com/projectcreate)
   con l'account da usare e crea un progetto, per esempio `reMarkable Drive`.
2. Abilita [Google Drive API](https://console.cloud.google.com/apis/library/drive.googleapis.com).
3. Apri [Google Auth Platform](https://console.cloud.google.com/auth/overview).
   Configura nome dell'applicazione, email di assistenza e destinatari esterni.
   Per la prova aggiungi il tuo indirizzo tra gli utenti di test.
4. In **Clients → Create client**, scegli **Desktop app**. Scarica il JSON e
   conservalo sul computer, fuori dal repository. Non scegliere Web application,
   TV, service account o Android.
5. Dal pacchetto di sviluppo esegui:

   ```powershell
   .\drive-cloud-setup.exe authorize --client "C:\percorso\client.json" --host IP_TABLET
   ```

   Su macOS/Linux il comando è `./drive-cloud-setup authorize --client /percorso/client.json --host IP_TABLET`.
   Il programma apre un listener su `127.0.0.1`, mostra un collegamento Google
   protetto con PKCE e trasferisce il token tramite SSH. Conferma la chiave SSH
   del tuo tablet prima di inviare credenziali. Nessun token viene scritto nei log.

Il permesso `drive` è ampio: Google non offre un'autorizzazione a una sola
cartella che includa automaticamente i nuovi file caricati da altre applicazioni.
Il motore controlla ID, appartenenza e marcatori della propria cartella prima
di modificarla. Gli altri documenti del tuo Drive non vengono gestiti.

**Configurazione di test:** per app OAuth esterne in Testing Google può far
scadere il refresh token dopo sette giorni. Non considerare questa configurazione
un collegamento permanente. Prima del collaudo continuativo completa la
configurazione per uso personale in Production, secondo le regole Google:

1. Completa **Branding** con nome, assistenza, contatto dello sviluppatore e i
   riferimenti pubblici richiesti. La pagina del progetto è
   `https://github.com/noire342/remarkable-drive-cloud`; l'informativa è
   [qui](PRIVACY.md). Aggiungi il dominio dei riferimenti se la console lo richiede.
2. In **Pubblico**, controlla l'account e passa da **Test** a **In produzione**.
   Google può ancora mostrare l'avviso di applicazione non verificata. Lo stato
   Produzione rende il client autorizzabile da altri account che ne conoscano
   l'identificativo; non limita gli accessi alla precedente lista di utenti test.
   Conserva privatamente la configurazione del tuo client personale.
3. Esegui nuovamente `authorize` per ottenere un'autorizzazione emessa dopo il
   cambio di stato. Verifica l'account collegato sul tablet.

La pubblicazione OAuth non equivale alla verifica Google e non pubblica il JSON
del client o i token. Non esiste un client OAuth condiviso con gli altri utenti
di questo progetto. Un token può comunque essere revocato dall'utente o da Google.

Il collegamento Google e la creazione dell'archivio sono separati dalla migrazione:
**autorizzare Drive non sposta, elimina o scollega i documenti del vecchio cloud.**

Fonti: [OAuth desktop](https://developers.google.com/identity/protocols/oauth2/native-app),
[permessi Drive](https://developers.google.com/workspace/drive/api/guides/api-specific-auth),
[scadenza token](https://developers.google.com/identity/protocols/oauth2#expiration).
