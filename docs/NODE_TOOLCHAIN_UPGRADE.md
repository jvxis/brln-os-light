# One-time Node/npm preparation for older LightningOS installations

## English

**Release preparation: the commands below become available only after the
0.5.48-Beta source tag is published. Do not run them against an unpublished tag.**

Before upgrading **0.5.47 or earlier** to **0.5.48 or later**, prepare the private
LOS build toolchain once. Do not rerun an installer over an existing LOS node.
Stop here if an upgrade is already running.

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash
```

Proceed only after the final `[OK] Ready` message. Then upgrade normally through
the LightningOS UI. On failure, do not start the upgrade: preserve the error for
support and retry the preparation after resolving its cause. Download failures,
invalid checksums or unsafe paths are not bypassed with system Node.

The script downloads official Node.js **24.22.1**, including npm **11.19.1**,
verifies a release-pinned SHA-256 and checks both executables. It stores them
under `/opt/lightningos/toolchains/node-v24.22.1-linux-<architecture>`.
It configures PATH **only for lightningos-app-upgrade.service**, using
`/etc/systemd/system/lightningos-app-upgrade.service.d/20-lightningos-node.conf`.
It does not replace global Node/npm, edit shell profiles or APT sources, update
Docker images, start an upgrade or restart Manager, Bitcoin, LND or apps.
Existing global Node/npm remain the operating system/operator's responsibility;
this is not a general host security update.

For a read-only verification after preparation:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash -s -- --check
```

The process is repeatable. An existing invalid managed toolchain or a conflicting
drop-in is rejected rather than repaired by changing unrelated permissions.
The compatibility drop-in may remain installed: 0.5.48+ updaters explicitly select
the target release's toolchain. No recurring manual Node/npm update is intended.

New installations through `lo_bootstrap.sh`, `install.sh`,
`install_existing.sh` or `install_existing_pi.sh` from a published 0.5.48+
release prepare their private build tools automatically.
Nodes **below 0.5.40 still must upgrade through 0.5.40 first**. This preparation
does not remove that mandatory bridge or fix unrelated legacy permissions.

Supported binaries: native Linux x86-64 and ARM64; Python 3, curl, tar, sha256sum,
flock and systemd are required for standalone preparation. This does not create
a Node service or expose a new port. Downloads use the official nodejs.org
distribution; no NodeSource setup script, nvm or npm global update is executed.

Operator rollback, only when no upgrade is running: remove **only**
`/etc/systemd/system/lightningos-app-upgrade.service.d/20-lightningos-node.conf`,
then run `sudo systemctl daemon-reload`. Do not delete the toolchain or change
global Node to diagnose a failed upgrade. Removing the drop-in makes old
updaters use their original system Node again.

## Português

**Preparação da release: os comandos abaixo só estarão disponíveis depois da
publicação da tag 0.5.48-Beta. Não execute contra uma tag ainda não publicada.**

**ATENÇÃO: quem estiver na 0.5.47 ou anterior**, antes de atualizar para a
0.5.48 ou posterior, execute uma vez:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash
```

Não execute durante outro upgrade. Aguarde a mensagem final `[OK] Ready` e só
então atualize normalmente pela UI. Se falhar, guarde o erro e resolva a causa
antes de tentar novamente; não execute o instalador por cima do LOS existente.

O script prepara Node **24.22.1** e npm **11.19.1** em um diretório privado do LOS,
verificando o SHA-256 fixado e o funcionamento dos executáveis. Ajusta somente
o PATH do serviço de upgrade. **Não altera Node/npm global, APT, perfis do shell,
imagens Docker ou configurações dos apps; não reinicia serviços e não dispara
upgrade.** O Node global continua sob responsabilidade do sistema/operador.

Para verificar a preparação sem alterar arquivos:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash -s -- --check
```

É possível repetir o script. Ele recusa arquivos conflitantes, permissões
inseguras ou uma instalação privada inválida, sem tentar corrigir permissões de
outros componentes. O ajuste de compatibilidade fica restrito a
`/etc/systemd/system/lightningos-app-upgrade.service.d/20-lightningos-node.conf`.
Versões 0.5.48+ preparam automaticamente o ambiente da release de destino;
não é necessário um comando manual a cada atualização.

Instalações novas pelos três instaladores, inclusive via bootstrap, já fazem a
preparação automaticamente a partir da release publicada 0.5.48.
**Nodes anteriores à 0.5.40 continuam obrigados a passar pela 0.5.40.**
O script não corrige outros problemas de permissões ou instalações antigas.

Para desfazer apenas a compatibilidade, sem upgrade em execução, remova somente
o arquivo `20-lightningos-node.conf` no caminho acima e execute
`sudo systemctl daemon-reload`. Preserve as ferramentas privadas e o Node
global. O atualizador antigo voltará a usar o Node original do sistema.
