# Systemd Units (v0.2)

## lnd.service (template)
[Unit]
Description=Lightning Network Daemon (LND)
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/lnd --lnddir=/home/lnd/.lnd
ExecStop=/usr/local/bin/lncli stop
Restart=on-failure
RestartSec=60
Type=notify
TimeoutStartSec=1200
TimeoutStopSec=3600
User=lnd
Group=lnd

[Install]
WantedBy=multi-user.target

## lightningos-manager.service (template)
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
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd

[Install]
WantedBy=multi-user.target

## lightningos-terminal.service (template)
[Unit]
Description=LightningOS Web Terminal
After=network-online.target
Wants=network-online.target

[Service]
User=losop
Group=losop
EnvironmentFile=/etc/lightningos/terminal.env
ExecStart=/usr/local/sbin/lightningos-terminal
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target

## lightningos-reports.service (template)
[Unit]
Description=LightningOS Reports Reconciliation
After=network-online.target lnd.service postgresql.service
Wants=network-online.target

[Service]
User=lightningos
Group=lightningos
SupplementaryGroups=systemd-journal
Type=oneshot
Environment=REPORTS_RUN_TIMEOUT_SEC=600
EnvironmentFile=/etc/lightningos/secrets.env
ExecStart=/opt/lightningos/manager/lightningos-manager reports-reconcile --config /etc/lightningos/config.yaml
LimitNOFILE=65536
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd

[Install]
WantedBy=multi-user.target

## lightningos-reports.timer (template)
[Unit]
Description=LightningOS Reports Reconciliation Timer

[Timer]
OnCalendar=*-*-* *:05:00
Persistent=true
Unit=lightningos-reports.service

[Install]
WantedBy=timers.target
