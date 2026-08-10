package appmanifest

import "fmt"

const (
	RoboSatsID             = "robosats"
	RoboSatsProject        = "robosats"
	RoboSatsComposeFile    = "docker-compose.yaml"
	RoboSatsPrimaryService = "robosats"
	RoboSatsCaddyfileFile  = "Caddyfile"
	RoboSatsTLSDir         = "tls"
	RoboSatsPort           = 12596

	// RoboSatsImage is pinned to the self-hosted-working release. v0.8.5-alpha
	// regressed by leaving the order screen on its loading state.
	RoboSatsImage    = "recksato/robosats-client:v0.8.4-alpha"
	RoboSatsTorImage = "osminogin/tor-simple:0.4.9.5"
	// RoboSatsProxyImage terminates TLS/HTTP2 so slow Tor coordinator polls do
	// not starve the Nostr relay WebSockets behind the browser connection limit.
	RoboSatsProxyImage = "caddy:2.8-alpine"

	RoboSatsImageClient AppImageVariant = "client"
	RoboSatsImageTor    AppImageVariant = "tor"
	RoboSatsImageProxy  AppImageVariant = "proxy"
)

var roboSatsImages = map[AppImageVariant]string{
	RoboSatsImageClient: RoboSatsImage,
	RoboSatsImageTor:    RoboSatsTorImage,
	RoboSatsImageProxy:  RoboSatsProxyImage,
}

func RoboSatsImages() []string {
	return []string{RoboSatsImage, RoboSatsTorImage, RoboSatsProxyImage}
}

func RoboSatsImageVariants() []AppImageVariant {
	return []AppImageVariant{RoboSatsImageClient, RoboSatsImageTor, RoboSatsImageProxy}
}

func RoboSatsImageForVariant(variant AppImageVariant) (string, error) {
	image, ok := roboSatsImages[variant]
	if !ok {
		return "", fmt.Errorf("robosats image variant is not allowed")
	}
	return image, nil
}

func RoboSatsCompose(caddyfilePath string, tlsDir string) string {
	return fmt.Sprintf(`services:
  tor:
    image: %s
    restart: unless-stopped
    volumes:
      - tor-data:/var/lib/tor
      - tor-log:/var/log/tor
  robosats:
    image: %s
    user: "0:0"
    restart: unless-stopped
    depends_on:
      - tor
    environment:
      TOR_PROXY_IP: tor
      TOR_PROXY_PORT: 9050
    volumes:
      - robosats-data:/usr/src/robosats/data
  proxy:
    image: %s
    restart: unless-stopped
    depends_on:
      - robosats
    ports:
      - "%d:%d"
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/tls:ro
      - caddy-data:/data
      - caddy-config:/config
volumes:
  robosats-data:
  tor-data:
  tor-log:
  caddy-data:
  caddy-config:
`, RoboSatsTorImage, RoboSatsImage, RoboSatsProxyImage, RoboSatsPort, RoboSatsPort, caddyfilePath, tlsDir)
}

func RoboSatsCaddyfile() string {
	return fmt.Sprintf(`{
	admin off
	auto_https off
}

https://:%d {
	tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key
	reverse_proxy robosats:%d {
		transport http {
			response_header_timeout 8s
		}
	}
}
`, RoboSatsPort, RoboSatsPort)
}
