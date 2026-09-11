# LOS Mesh

Optional native Meshtastic USB integration. The Manager handles Bitcoin mainnet
transaction previews, signing, relay policy, invoices and user-approved Lightning
payments. `lightningos-mesh` is an unprivileged serial bridge with no Bitcoin/LND
credentials, IP networking, generic broker access or wallet signing capability.

## Setup

1. Upgrade LightningOS normally. Both installers and the upgrade helper build
   the bridge binary; they do **not** enable the optional app or create its user.
2. Connect a Meshtastic radio using USB. In a VM, enable USB passthrough first.
3. Open LOS Mesh in the App Store, select its `/dev/serial/by-id/...` device and
   choose send-only (default), relay-only or bidirectional operation. Confirm
   locally with the LightningOS password. Installation requires a successful
   Meshtastic serial identity response; failure removes the incomplete service.
4. Configure each endpoint with the other radio's `!xxxxxxxx` node ID and the
   same cryptographically random 32-byte pairing key. Transfer this key through
   a separate secure channel and verify its fingerprint. Save at both endpoints;
   repeat pairing if the first hello arrived before the other contact existed.
5. Grant relay permission explicitly on the receiving endpoint when it should
   publish signed transactions from that contact. There is no open public relay.

The root-controlled device configuration is stored in `/var/lib/lightningos-mesh/device.json`.
The broker creates the no-shell `losmesh` user only when the optional app is
installed. It grants an ACL on the selected character device, plus a matching
systemd `DeviceAllow`. It never joins `dialout` or an LND/Manager group. If a USB
reconnection changes the device's minor number or removes its ACL, reconnect
the radio through the app to revalidate the stable identity and permissions.
Start, stop and uninstall use fixed broker operations. Uninstall removes the
service/configuration/selected device ACL and pairing keys, preserving metadata
history. It retains the inactive service account and packaged bridge executable.

**Radio configuration is preserved.** LOS Mesh does not flash firmware, change
region/channel/security settings, reset the radio or issue Meshtastic admin
commands. Configure matching region, channel and a firmware supporting the serial
client API separately. Do not interpret USB connectivity as a successful LoRa
end-to-end test; that requires two compatible radios and two endpoints.

## Transactions and requests

- Import an already signed raw transaction, or fund an unsigned PSBT from
  confirmed LND UTXOs. A funded preview reserves inputs and expires in two minutes.
  It shows recipient, amount, exact fee, change and debit. Imported transactions
  show outputs/TXID/size; their fee cannot be verified without the previous outputs.
- Signing requires local confirmation and reauthentication. WalletKit
  `FinalizePsbt` signs without publishing. Only the remote authorized relay calls
  `PublishTransaction`, after live Bitcoin mainnet readiness and structural checks.
- Signing/transmitting is consequential: cancelling a transfer does not revoke
  a signature already delivered. Signed input leases remain for the transfer
  window; after a restart LND's lease expiry still applies. Never assume a cancelled
  or interrupted radio session means that its transaction cannot be published.
- Address/amount payment requests and mainnet BOLT11 invoices can travel in either
  direction. Received invoices remain in memory pending local approval. Payment
  requires review of destination, amount, expiry, hash and maximum routing fee,
  reauthentication, and the existing spending guard. LND uses `SendPaymentV2` over
  the ordinary Lightning network; HTLCs and channel state do not travel over LoRa.
- BOLT11 amounts must be positive whole sats, up to 100,000,000 sats. Zero-amount
  invoices, BOLT12, keysend, remote commands and public relays are unsupported.
- Publication/payment uncertainty is recorded explicitly and is never retried
  automatically. Consult wallet activity or the transaction explorer. Relay
  publication is not blockchain confirmation.

## Protocol v1

Meshtastic private application port **256**; LOS Mesh is not a registered port.
Frames are unicast to an explicitly paired node. Each encrypted packet is at most
228 bytes: 92-byte authenticated header, up to 120 content bytes, 16-byte GCM tag.

All multibyte application integers use network byte order:

| Offset | Size | Field |
|---|---:|---|
| 0 | 4 | `LOSM` magic |
| 4 | 1 | Version = 1 |
| 5 | 1 | Message kind |
| 6 | 1 | Bitcoin mainnet domain = 0 |
| 7 | 1 | Reserved = 0 |
| 8 | 4 | Sender Meshtastic node number |
| 12 | 4 | Destination node number |
| 16 | 16 | Random session ID |
| 32 | 2 | Zero-based fragment index |
| 34 | 2 | Total fragments (data messages only) |
| 36 | 4 | Total content length (data messages only) |
| 40 | 32 | SHA-256 of complete content |
| 72 | 8 | Expiry, Unix seconds |
| 80 | 12 | Fresh random AES-GCM nonce for every transmission |

AES-256-GCM uses the independently provisioned pairing key, the entire header as
associated data, and the fresh nonce. The GCM tag supplies authenticated integrity
for each fragment; SHA-256 verifies complete content. Kind 1 = hello, 2 = hello ACK,
3 = signed transaction, 4 = chunk ACK, 5 = result, 6 = cancel, 7 = invoice,
8 = JSON address payment request. Replies echo session/hash/index, swap peers and
use a new nonce. Control messages set total/size to zero. The serial adapter checks
the actual Meshtastic sender/destination against the authenticated envelope.

Data sessions expire after 20 minutes; packets more than 30 minutes in the future
are rejected. Keep endpoint clocks synchronized. A sender keeps one fragment
outstanding and retries after 12 seconds, at most three transmissions. The bridge
enforces a two-second minimum transmit interval and bounded queues (16 TX, 64 RX).
ACK/NACK describes application receipt or outcome, separately from Meshtastic ACK.

Limits: 16 contacts, 128 fragments / 15,360 bytes per message, 8 inbound assemblies,
4 outbound sessions, 4 unsigned previews, 8 outstanding leased transfers, 8 new
inbound data sessions per peer/hour, 1,000 session records and 1,000 publication or
payment deduplication records each. Ledgers retain 30 days and refuse new work
when full. The UI shows the latest 100 metadata records. Raw transactions, complete
invoices, pairing keys and preimages are excluded from ordinary logs/history.
Incomplete sessions become interrupted on Manager restart; they do not silently
resume or republish. Pairing secrets are private Manager database records and
never supplied to the serial bridge.

## Process boundary

`/run/lightningos-mesh/bridge.sock` exposes only `GET /status`, `GET /packets` and
`POST /packets`. Linux `SO_PEERCRED` restricts incoming connections to the fixed
Manager UID (or root). The Manager independently verifies the peer is `losmesh`.
The socket is connectable by local users but rejects unauthorized UIDs before HTTP
parsing. No arbitrary serial bytes, device settings or admin commands are exposed.
The daemon has strict systemd filesystem/device/memory/task restrictions and only
`AF_UNIX`. The broker validates root-controlled files, the dedicated account and
stable USB identity. Updating a selected device restarts only the mesh service.

## Wire adapter provenance and licenses

The adapter independently encodes only the public Protobuf wire fields necessary
for `ToRadio.packet`, `ToRadio.want_config_id`, `FromRadio.packet` and local node
identity. It uses the repository's existing pinned
`google.golang.org/protobuf v1.36.5`; no additional SDK or generated Meshtastic
source was imported. The inspected upstream schema revision is
`f008c459d78de46779408d5bdb7bc0634550bb49` (Meshtastic protobuf repository, GPL-3.0).
The project does not vendor that schema or generated code. btcmesh (MIT) was a
conceptual reference; LOS Mesh has its own versioned protocol and does not claim
wire compatibility. The LOS Mesh antenna icon is original project artwork and
does not use the Meshtastic trademark as the app's own logo.

References:
- https://meshtastic.org/docs/development/device/client-api/
- https://github.com/meshtastic/protobufs/tree/f008c459d78de46779408d5bdb7bc0634550bb49
- https://meshtastic.org/docs/development/firmware/portnum/
- https://github.com/pagcoinbr/btcmesh

## Validation

Run `go test ./...` and `npm run build`. Protocol tests cover altered header/content,
wrong key/peer/domain, invalid coordinates, out-of-order/duplicate/missing packets,
hash conflicts, serial resynchronization, malformed commands and USB disconnect.
Fuzz targets are `FuzzAuthenticatedParser` and `FuzzRadioParser`.

Persistent relay/replay/restart tests require an isolated PostgreSQL database whose
name starts with `los_mesh_test_`. Set `LOS_MESH_TEST_DSN` and run
`go test ./internal/server -run '^TestMesh'`. Those tests truncate only their
guarded test database and use a simulated wallet: they never publish or pay on
Bitcoin mainnet. Physical acceptance requires two radios; separately validate
WalletKit signing/publication and Lightning payment on a controlled funded fixture.

## Guided pairing (LOSP v1)

The radio NodeDB populates contact selection; names and last-heard times are
untrusted hints. Only explicit, bounded unicast probes are transmitted. A
correlated response advertises LOS Mesh availability, not verified identity.
The private application port carries a separate LOSP envelope, never an admin
command or device setting. Legacy/manual LOSM contacts remain supported.

Both operators accept and independently compare an eight-digit SAS. Pairing
uses fresh X25519 keys and 32-byte random opening nonces, SHA-256 commitments
bound to sender/recipient/session/expiry, then key reveals after both commitments
are fixed. Transcript-bound HMAC-SHA256 extract/expand derives the application
key; separate domain labels derive the SAS and directional confirmation MACs.
The code is not a password or a shared key and must not be compared over the
same unverified radio channel. No TOFU or channel-key fallback grants trust.
Both local approval and authenticated remote approval are required. Newly
verified contacts always have relay disabled; existing contacts are never
silently replaced. Key rotation requires contact removal and fresh pairing.

Bounds: 256 cached radio nodes, 8 live pairing sessions, 128 recent session IDs,
256 per-node rate records; five-minute exchange expiry; up to 12 bounded
retransmissions at 12-second intervals, in addition to the bridge's global
radio pacing. Incomplete exchanges are memory-only and disappear on restart;
only mutually verified application keys enter the existing private contact DB.

Validation includes transcript alteration, reflection, expiry, duplicate
messages, two simulated operators with PostgreSQL persistence, invite flood
limits and cancelled-session replay. A physical bidirectional acceptance test
still needs a second LOS Mesh endpoint and radio; simulation is not a substitute.
