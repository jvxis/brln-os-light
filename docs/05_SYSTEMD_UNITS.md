# FILE: docs/05_SYSTEMD_UNITS.md

# Templates systemd (v0.1)

## /etc/systemd/system/lnd.service
[Unit]
Description=Lightning Network Daemon (LND)
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/lnd
ExecStop=/usr/local/bin/lncli stop

# Gerenciamento de processo
Restart=on-failure
RestartSec=60
Type=notify
TimeoutStartSec=1200
TimeoutStopSec=3600

# Criacao e permissoes de diretorio
RuntimeDirectory=lightningd
RuntimeDirectoryMode=0710
User=lnd
Group=lnd

# Hardening (ajustar conforme necessario)
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
MemoryDenyWriteExecute=true
ProtectSystem=full

[Install]
WantedBy=multi-user.target

## /etc/systemd/system/lightningos-manager.service
[Unit]
Description=LightningOS Manager
After=network-online.target lnd.service postgresql.service
Wants=network-online.target

[Service]
User=lightningos
Group=lightningos
Type=simple
EnvironmentFile=/etc/lightningos/secrets.env
ExecStart=/opt/lightningos/manager/lightningos-manager --config /etc/lightningos/config.yaml
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd

[Install]
WantedBy=multi-user.target
