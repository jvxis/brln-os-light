# FILE: docs/05_SYSTEMD_UNITS.md

# Templates systemd (v0.1)

## /etc/systemd/system/lnd.service
[Unit]
Description=Lightning Network Daemon (LND)
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
User=lnd
Group=lnd
Type=simple
ExecStart=/usr/local/bin/lnd
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# Hardening (ajustar conforme necessário)
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=false
ReadWritePaths=/data/lnd /var/log/lnd
# Se precisar acessar /home/lnd/.lnd, ajustar paths.

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

