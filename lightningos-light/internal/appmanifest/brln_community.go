package appmanifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// BR⚡LN Community is the members chat of the BR⚡LN Club. The node
// runs a local copy of the chat and the member's Nostr remote signer (NIP-46).
// The key never leaves the signer data directory; the chat relay is operated by
// the club and is reached over an outbound WebSocket only.
//
// The node also runs its own NIP-46 pairing relay next to the signer, so the chat
// and the signer on the same node do not need the club's server to talk to each
// other. The club's pairing relay stays in every pairing link for devices that
// cannot reach the node.
const (
	BRLNCommunityID              = "brln-community"
	BRLNCommunityProject         = "brln-community"
	BRLNCommunityComposeFile     = "docker-compose.yaml"
	BRLNCommunityCaddyfile       = "Caddyfile"
	BRLNCommunityTLSDir          = "tls"
	BRLNCommunityPrimaryService  = "proxy"
	BRLNCommunityStopTimeout     = 30
	BRLNCommunityPort            = 4448
	BRLNCommunityWebInternalPort = 8080
	BRLNCommunitySignerPort      = 8081
	BRLNCommunitySignerRelay     = "wss://signer.br-ln.com"
	BRLNCommunityPairingPort     = 3335
	BRLNCommunityPairingPath     = "/pairing"

	BRLNCommunityWebUID    = 101
	BRLNCommunityWebGID    = 101
	BRLNCommunitySignerUID = 65529
	BRLNCommunitySignerGID = 65529
	BRLNCommunityProxyUID  = 65532
	BRLNCommunityProxyGID  = 65532
	// The pairing relay runs from the signer image but never as the signer: it holds
	// nothing and mounts nothing, and a separate identity keeps it that way.
	BRLNCommunityPairingUID = 65528
	BRLNCommunityPairingGID = 65528

	BRLNCommunityRelease             = "0.1.69"
	BRLNCommunityWebDigest           = "4a7ec3e8203f211013dbf6dbdfbe141c79acac7a984eda2b4565f7be8bc919e7"
	BRLNCommunitySignerDigest        = "a35e6c2feec83b78b47e04c2d0ca51a2ab776e9cf467a15d0f82f3706dca1f32"
	BRLNCommunitySignerVersionOutput = "brln-signer " + BRLNCommunityRelease

	BRLNCommunityWebImage    = "ghcr.io/jvxis/brln-community-web:" + BRLNCommunityRelease + "@sha256:" + BRLNCommunityWebDigest
	BRLNCommunitySignerImage = "ghcr.io/jvxis/brln-signer:" + BRLNCommunityRelease + "@sha256:" + BRLNCommunitySignerDigest
	// The proxy is the same official Caddy release Bark Wallet pins.
	BRLNCommunityProxyImage = BarkWalletProxyImage

	BRLNCommunityImageWeb    AppImageVariant = "web"
	BRLNCommunityImageSigner AppImageVariant = "signer"
	BRLNCommunityImageProxy  AppImageVariant = "proxy"
)

type BRLNCommunityComposePaths struct {
	SignerDir            string
	AuthDir              string
	CaddyfilePath        string
	TLSCertificate       string
	TLSPrivateKey        string
	ManagerCACertificate string
}

func BRLNCommunityImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case BRLNCommunityImageWeb:
		return BRLNCommunityWebImage, nil
	case BRLNCommunityImageSigner:
		return BRLNCommunitySignerImage, nil
	case BRLNCommunityImageProxy:
		return BRLNCommunityProxyImage, nil
	default:
		return "", errors.New("BR⚡LN Community image variant is not allowed")
	}
}

func BRLNCommunityImageVariants() []AppImageVariant {
	return []AppImageVariant{BRLNCommunityImageWeb, BRLNCommunityImageSigner, BRLNCommunityImageProxy}
}

func BRLNCommunityImages() []string {
	return []string{BRLNCommunityWebImage, BRLNCommunitySignerImage, BRLNCommunityProxyImage}
}

// BRLNCommunityCaddyConfig serves the chat at / and the signer at /signer/.
// Signer calls that set up or export the key, or pair a new device, also need a
// recent LightningOS reauthentication through forward_auth.
func BRLNCommunityCaddyConfig() string {
	return fmt.Sprintf(`{
	admin off
	auto_https off

	# HTTP/3 brings nothing on a LAN and breaks the browser exception for the
	# app's self-signed certificate: the page loads, then QUIC requests fail.
	servers {
		protocols h1 h2
	}
}

https://:%d {
	tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key

	route {
		redir /signer /signer/ 308

		@signer_sensitive path /signer/api/key /signer/api/backup /signer/api/pairings /signer/api/nostrconnect
		handle @signer_sensitive {
			forward_auth https://host.docker.internal:8443 {
				uri /api/apps/brln-community/signer-authorization
				header_up -Authorization
				transport http {
					tls_trust_pool file /etc/caddy/manager-ca.crt
					tls_server_name localhost
				}
			}
			uri strip_prefix /signer
			reverse_proxy signer:%d
		}

		handle_path /signer/* {
			reverse_proxy signer:%d
		}

		# The node's own pairing relay. It only carries NIP-46 messages, which are
		# end-to-end encrypted between device and signer, so it sits beside the chat
		# rather than behind the LightningOS reauthentication.
		@pairing path %s %s/
		handle @pairing {
			rewrite * /
			reverse_proxy pairing:%d
		}

		handle {
			reverse_proxy web:%d
		}
	}
}
`, BRLNCommunityPort, BRLNCommunitySignerPort, BRLNCommunitySignerPort,
		BRLNCommunityPairingPath, BRLNCommunityPairingPath, BRLNCommunityPairingPort,
		BRLNCommunityWebInternalPort)
}

// brlnCommunityProxyConfigHash pins the proxy service to its configuration, so a
// change in the Caddyfile recreates the container. Compose only compares service
// definitions, never the contents of a bind-mounted file.
func brlnCommunityProxyConfigHash() string {
	sum := sha256.Sum256([]byte(BRLNCommunityCaddyConfig()))
	return hex.EncodeToString(sum[:8])
}

func BRLNCommunityCompose(paths BRLNCommunityComposePaths) (string, error) {
	if paths.SignerDir == "" || paths.AuthDir == "" || paths.CaddyfilePath == "" || paths.TLSCertificate == "" ||
		paths.TLSPrivateKey == "" || paths.ManagerCACertificate == "" {
		return "", errors.New("BR⚡LN Community compose path is invalid")
	}
	return fmt.Sprintf(`services:
  web:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777

  signer:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      DATA_DIR: /data
      LISTEN: ":%d"
      RELAYS: %s
      LOCAL_RELAY: ws://pairing:%d
      LOCAL_RELAY_PATH: %s
      KEY_PASSWORD_FILE: /run/lightningos-auth/key_password
      ACCESS_PASSWORD_FILE: /run/lightningos-auth/access_password
    volumes:
      - %s:/data:rw
      - %s:/run/lightningos-auth:ro
    depends_on:
      - pairing

  pairing:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    entrypoint:
      - /brln-signer-relay
    environment:
      PORT: "%d"

  proxy:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      CONFIG_HASH: %s
    entrypoint:
      - /bin/sh
      - -c
    command:
      - |
        set -eu
        cp /usr/bin/caddy /run/lightningos-bin/caddy
        chmod 0500 /run/lightningos-bin/caddy
        exec /run/lightningos-bin/caddy run --config /etc/caddy/Caddyfile
    ports:
      - "%d:%d"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
      - /run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=%d,gid=%d,mode=0700
      - /data:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
      - /config:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/tls/server.crt:ro
      - %s:/etc/caddy/tls/server.key:ro
      - %s:/etc/caddy/manager-ca.crt:ro
    depends_on:
      - web
      - signer
      - pairing

networks:
  default:
    name: brln-community_default
`, BRLNCommunityWebImage, BRLNCommunityStopTimeout, BRLNCommunityWebUID, BRLNCommunityWebGID,
		BRLNCommunitySignerImage, BRLNCommunityStopTimeout, BRLNCommunitySignerUID, BRLNCommunitySignerGID,
		BRLNCommunitySignerPort, BRLNCommunitySignerRelay,
		BRLNCommunityPairingPort, BRLNCommunityPairingPath,
		paths.SignerDir, paths.AuthDir,
		BRLNCommunitySignerImage, BRLNCommunityStopTimeout, BRLNCommunityPairingUID, BRLNCommunityPairingGID,
		BRLNCommunityPairingPort,
		BRLNCommunityProxyImage, BRLNCommunityStopTimeout, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		brlnCommunityProxyConfigHash(),
		BRLNCommunityPort, BRLNCommunityPort,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		paths.CaddyfilePath, paths.TLSCertificate, paths.TLSPrivateKey, paths.ManagerCACertificate), nil
}

// BRLNCommunityImagePair is the web and signer image pair one app release pinned.
type BRLNCommunityImagePair struct {
	Release      string
	WebDigest    string
	SignerDigest string
}

func (pair BRLNCommunityImagePair) WebImage() string {
	return "ghcr.io/jvxis/brln-community-web:" + pair.Release + "@sha256:" + pair.WebDigest
}

func (pair BRLNCommunityImagePair) SignerImage() string {
	return "ghcr.io/jvxis/brln-signer:" + pair.Release + "@sha256:" + pair.SignerDigest
}

// BRLNCommunityPreviousReleases are the image pairs earlier catalogs pinned, oldest first.
//
// A LightningOS upgrade brings a new catalog before the operator updates the app, so for a while
// the installed compose still names the previous pair. Reading the signer access password must
// keep working in that window: until 0.5.46 only the 0.1.4 pair of the 0.5.29 catalog was
// accepted, and every later catalog left the password unreadable ("admin password unavailable")
// until the app was updated (16/10/2026, LOS-TESTE2 and jvx-minipc01 with 0.1.57 installed under
// the 0.1.58 catalog). Every byte outside the two image pins must still match the current catalog.
//
// When BRLNCommunityRelease moves, the release it replaces must be here. The current release may
// already be listed (it is skipped), so a catalog bump and this list never have to land together.
var BRLNCommunityPreviousReleases = []BRLNCommunityImagePair{
	{"0.1.0", "ed3a736ce4c60c209728bfe44c5172dde49cd82eb0b720bc61c0a4202a436715", "2522cca0decdcc04af513acd2138b6ef1a22b06d41eb697788861d73ad774985"},
	{"0.1.1", "d67d5ed74ab0a9001cf8aafccb387d30f45dbf538a77110e4482b288af582d6a", "f6052f613f712ca32e17e4d4a83afbb97603f355f93b97dfcbd51f447ac65e70"},
	{"0.1.2", "a0a1c4a6d44473de5899910c771ecdc5747e1ddc2eae02bcb9647a721c9664d4", "8efadf74db1c956697ba40ee582e65199b7c1d14862ddc4270fbe6634d0fc646"},
	{"0.1.3", "ef72fc088fc7e22b465ebbfba59cabf93559e57fa797398efa7920fd2e535aa1", "e55b1e111955103438b8aa7aec1c9418cd1cc92da2c71fa6b61a830aa2eb3acd"},
	{"0.1.4", "3aec012bfdb29f3e4cf6f7626b25333a8505844c56decb6d522ffbc211cd68d6", "c1c5b282cd0fa5ea5720f7c021a5fa32da6c7a00b1e45552193d207c89a3b2bc"},
	{"0.1.15", "c53906dab41382b0f5066c884d56aea59a548b0249a722b193354fdd15bec75e", "f89de43907150ee6dd724e608308af36f6fe2a46a851c32e2f4e5f6cd8efb8ad"},
	{"0.1.16", "1475abb9fad29c20a0b404d1ebe4d60c11a8063079338db14fb9722374a9f5a6", "e333d60963e29591b1a79e3185f0bc2eb99dade6c3c823322f506fdd37bfa342"},
	{"0.1.18", "3a8c1e74d365c0cc02e17aac0e7c5b3e5b7e81ebe3b16ee568accc971567ba57", "414987115257a54c4e5038896d040b19f082fc97800496beb14de6645fe41b0d"},
	{"0.1.19", "91d7dfd74d1b8742d4635d24d093a11d68d4de548e5bf509d002cd025e0e9353", "72f137363f9a3584a5c9ba50bc2fe99dbef651f02716aa9462b5a968d44569e5"},
	{"0.1.22", "ba64ee6fbb33659a64efbe76e1b22b55e07addd0a505cff2be0ff2378c35e8b5", "be89d77b816b8c98b57d9565ec33c7cb9683bff92ce0237251d5a910d418e4ca"},
	{"0.1.23", "ef7d05a3e0f924382f3034d680f9b77827f5ec80f9ea7e4b65794d6c0380fb49", "2d1c572d4c50254ab441c7580c64fbcf46a7b33e3512e7a870a220ee874c5bd9"},
	{"0.1.24", "774281ebe5fac030211719f1d2ff4e36acc8953fa89f8de91ffa228f8378293d", "a84982a55c2b8e14115ace6e4a9c0a89e04304365e1f6b0f3e6bdcf172a4402b"},
	{"0.1.25", "eae710cc1caf92236dce8f7d28cc55c8fbadd13b7d4dcfda9d55305fa8a46d33", "ed67d2effe05a3b84d6217ab6be958641449dfee4474cf804803d13901ba8c0b"},
	{"0.1.26", "799bfbd2c741e3825bbdf3af7f1b42834bf71f3b1ad1d4ecf993e905d73859a8", "726ab1d5709dbb2649e562cf4c8ab11980ea979f7319ec79687e1fb09a750b58"},
	{"0.1.28", "335ccd68ab0d874fa55142271480350632e2d7865be41e3046b47762b22a2d4e", "6939e814f63543425f8d339d96a4501fd0e92cd89843e3045c4e5b50bc022302"},
	{"0.1.30", "df1b9cd23c236254d2411dc243d510df0c8a22aad972a15c25f65c917ae31769", "80c6a3f96a6ecd05e0aac5efe1dc7787cf1ea682a1525bf2575dae8d81bc6083"},
	{"0.1.33", "e6529601623c9d0baee44e318567a90f17225ad2de115e3aa759835de6392f8a", "b10855fac711cd6217398c6ffa85ec3dddcaa8ce7a8c50a1ba4552733af0ae4c"},
	{"0.1.34", "9e5f33cf4977d19d4b30d02676bd56b2e779da1a19a4839a0a99576b5267996b", "51d80995ba25941fffb4793c5dc6b02dc3deb6c7896e11a4e7004529c28c8006"},
	{"0.1.35", "94d53b261fe5fa0fe7d1fa98acac532eda48f75b10bc89c66df25d029207a43c", "90d2afb14f82347ea373a4351bfe7234ede1285996c610a1c8452a448e9dd26f"},
	{"0.1.36", "25ee463cab4b8bd583ce9ff7638cd721cfbce0fc717919b9915f342a2c89d8ea", "afef057e6de6d3a03f2e505fd136466ddcbc9c87d81614eafe77004f851ce8c3"},
	{"0.1.37", "56f1e86520f2e7f97051fd4fd579d6017793f6ea42aa30ea48db5f1a9ceefda2", "dd8e7397ffa6d1cf229b0fb1c2bfb22cc7be072ef806dbe16233b8ed288c6d45"},
	{"0.1.41", "fb84829451a86777f270f4dfc62a4fdcd2450053e32b0e2c5453b14846eab55e", "a749d274b14beff335d03372c2176960af415f356bf04114d0cea011a4ad0d59"},
	{"0.1.44", "b2c24ec4e1a1f005bf56a5acebb7b2adb46f02cdaf1d04f9dd7a75acda8a46f0", "bc3175dcdcc3cc980f802e0833c976802bf674e5d3b126d4cf9a8cb72a759adb"},
	{"0.1.46", "257fe7a791b6212db957b65451085f7c8394059c0f54104574575a9af0235dc2", "59dcb74e0bcce96fe15dd45c0dee80a367b7e057e9e6afc04c2d6ad1aa0e0d53"},
	{"0.1.48", "c7cd1acba12c1f0bdb6e1fba7ce65f85bac518e6be506d55645db0e5ef5244e2", "bef68f91a7aa3ae00d9947ff8630b2d94a10654ce3cc991ef002ddc96901e56c"},
	{"0.1.49", "e479d0f1940b3221f8264e168420fbeff86c0d2eb337c2adf5593ef36ecc4897", "b659b31ec231342176abc695c41848261e8178bbfc84a7b958c19d4b2485ea26"},
	{"0.1.53", "369572709edebe85ef6fdeca734a81d4039bf02dbae4c2c5dd25475a7e6bedee", "460e110fecf6ffecbf543d0ccd49511700e0f3e4097d92a4cf6b6242b9d4ed0c"},
	{"0.1.54", "2725ada601918fa323a9c5d920005b03a68cffe8e4c87c59da5092e9dce84537", "7ef97e934fa003357010bf598c735d98fde39067c301789e0077ec74b5dbc082"},
	{"0.1.55", "8680d04352af4ce839a9f02b8f206cc0d6ae00f26743139a4cd9f553a5736c03", "8b69550c3efe2f7002ae035622460b417d4b721f753ef13bc7324a697c5272a3"},
	{"0.1.56", "d2985e3db9a01d6f3e90ff49ba3eed71564b9814358249500c18a4d0209596b6", "49c272edc91ddd92dd36d047647cf41f499d8144bf4ec63f94bc38d39209a3e5"},
	{"0.1.57", "cc604bd7abe739bdc75941bd71de5edf0c87df6b45849b09542ffa1d0a3aa2e8", "12a0f89a5d4f1999314c1347dad45ad2e817656b23cd09282972cf662c622792"},
	{"0.1.58", "98944af254afc9c3ed5168dfe2ccb8961f7152dbb1b8aeb0f7536452d98634f2", "23a0b6383cf9944729db829a5cd04bc0e9ac6f8e7884a6c7be33701131055d37"},
	{"0.1.64", "d4a089e1c8a61484bbc079ccc4327e7ac12f7d554990878f8864e61b66b2fb53", "227e21f78cca34f599543172d8aa83147cae8cb6d656c298e3aa007c8008de3f"},
	{"0.1.68", "cacbe32e0dab1f5309fdd22a9e5866433a5a479c3866d995dd38b94e04191878", "7a808a6ac9d9ae28dc4c5d48b00551b87c2c51b84efffcddfb0c22482ce42473"},
}
