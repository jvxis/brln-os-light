# App Store Specification (v0.2)

## Overview
The App Store is a built-in catalog of optional services. Apps are defined in code (Go) and managed by the manager. Some apps use docker compose, while others are native binaries managed by systemd. The core system (LND and the manager) remains native and does not depend on Docker.

## Current apps
- bitcoincore: Local Bitcoin Core node (Docker)
- lndg: LNDg analytics dashboard (Docker)
- elements: Elements/Liquid node (native binary)

## App model
Apps are defined in Go via the appHandler interface:

- Definition() appDefinition
  - ID, Name, Description, Port
- Info(ctx) (appInfo, error)
- Install(ctx) error
- Uninstall(ctx) error
- Start(ctx) error
- Stop(ctx) error

The registry lives in:
- internal/server/apps_registry.go

The core types live in:
- internal/server/apps_types.go

## App storage layout
- /var/lib/lightningos/apps/<id>
  - docker-compose.yaml
  - .env (optional)
  - Dockerfile or entrypoint (optional)
- /var/lib/lightningos/apps-data/<id>
  - persistent data, database data, logs, secrets

Some apps use fixed paths (example: Bitcoin Core uses /data/bitcoin).

Native apps may use an additional fixed data directory under /data (example: Elements uses /data/elements).

## App lifecycle
Docker apps:
- Installed: compose file exists
- Status: derived from docker compose ps
- Start: docker compose up -d
- Stop: docker compose stop
- Uninstall: docker compose down and remove app files

Native apps:
- Installed: binary + systemd unit exist
- Status: derived from systemctl is-active
- Start/Stop: systemctl start/stop
- Uninstall: disable systemd unit and remove app files (data dir policy varies per app; Elements keeps /data/elements)

## Helpers you should reuse
- ensureDocker(ctx): installs docker and compose when needed
- runCompose(ctx, root, composePath, ...)
- getComposeStatus(ctx, root, composePath, service)
- ensureFileWithChange(path, content)
- readEnvValue, setEnvValue, readSecretFile, randomToken

## Adding a new app (step by step)

1) Create a new handler file:
- internal/server/apps_myapp.go

2) Define the app metadata:

Example:

package server

import "context"

type myApp struct { server *Server }

func newMyApp(s *Server) appHandler { return myApp{server: s} }

func myAppDefinition() appDefinition {
  return appDefinition{
    ID: "myapp",
    Name: "My App",
    Description: "Short description for the UI.",
    Port: 3000,
  }
}

func (a myApp) Definition() appDefinition { return myAppDefinition() }

3) Implement Info to return status:

func (a myApp) Info(ctx context.Context) (appInfo, error) {
  def := a.Definition()
  info := newAppInfo(def)
  paths := myAppPaths()
  if !fileExists(paths.ComposePath) {
    return info, nil
  }
  info.Installed = true
  status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "myapp")
  if err != nil {
    info.Status = "unknown"
    return info, err
  }
  info.Status = status
  return info, nil
}

4) Implement Install, Start, Stop, Uninstall:

func (a myApp) Install(ctx context.Context) error { return a.server.installMyApp(ctx) }
func (a myApp) Start(ctx context.Context) error { return a.server.startMyApp(ctx) }
func (a myApp) Stop(ctx context.Context) error { return a.server.stopMyApp(ctx) }
func (a myApp) Uninstall(ctx context.Context) error { return a.server.uninstallMyApp(ctx) }

5) Add the app to the registry:
- internal/server/apps_registry.go

apps := []appHandler{
  newBitcoinCoreApp(s),
  newLndgApp(s),
  newMyApp(s),
}

6) Add UI icon and optional route:
- ui/src/assets/apps/myapp.svg or myapp.ico
- ui/src/pages/AppStore.tsx
  - iconMap["myapp"] = myappIcon
  - internalRoutes["myapp"] = "some-route" (if internal)

## Example: minimal Docker app

Example compose contents:

services:
  myapp:
    image: ghcr.io/example/myapp:latest
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - /var/lib/lightningos/apps-data/myapp/data:/data

Example install flow:

type myAppPaths struct {
  Root string
  DataDir string
  ComposePath string
}

func myAppPaths() myAppPaths {
  root := filepath.Join(appsRoot, "myapp")
  dataDir := filepath.Join(appsDataRoot, "myapp", "data")
  return myAppPaths{
    Root: root,
    DataDir: dataDir,
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
  }
}

func (s *Server) installMyApp(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil { return err }
  paths := myAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil { return err }
  if err := os.MkdirAll(paths.DataDir, 0750); err != nil { return err }
  if _, err := ensureFileWithChange(paths.ComposePath, myAppCompose(paths)); err != nil { return err }
  return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

## Admin password support (optional)
- If your app needs an admin password helper, extend the server handlers.
- The current API supports this only for LNDg. Add new endpoints or generalize as needed.

## Validation checklist
- Unique app ID and port
- Compose file stored under /var/lib/lightningos/apps/<id>
- Status reports running, stopped, or not_installed
- UI shows icon and Open button if port is set
- Install, start, stop, uninstall are idempotent
