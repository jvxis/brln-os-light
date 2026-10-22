# Global Node/npm upgrade for dedicated LightningOS hosts

## English

**The commands below become available only after the 0.5.48-Beta source tag is
published. Do not run them against an unpublished tag.**

LightningOS assumes a dedicated host, including `install_existing` machines
with Bitcoin, LND and optional tools such as ThunderHub or Balance of Satoshis.
The 0.5.48+ installers and updater use the **global** Node/npm installation,
not a private build-only copy. New installations prepare it automatically.

Before upgrading **0.5.47 or earlier**, run this once, while no LOS upgrade is
running. Do not rerun the LOS installer over an existing installation:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash
```

Wait for `[OK] Ready`, then upgrade normally through the UI. On failure, preserve
the error and resolve its cause before upgrading. Nodes **below 0.5.40 still
must upgrade through 0.5.40 first**; this script does not bypass that bridge.

The script installs the release-tested Node.js **24.22.1** and its bundled npm
**11.19.1** using a NodeSource Debian package with a release-pinned SHA-256.
APT owns the resulting `/usr/bin/node`, `/usr/bin/npm` and package files.
No remote setup script is executed, and no APT source or shell profile is changed.
Future LOS installers/updaters apply their own reviewed Node/npm policy.

APT's plan is checked before installation: only `nodejs` may be installed or
upgraded, and only conflicting distro npm/development/documentation packages
may be removed. No autoremove is run. Missing dependencies, held packages,
unrelated removals/changes, an existing newer Node version, or a conflicting
manual global installation stop preparation rather than trigger a broad repair.
Install the reported prerequisites or resolve the conflict before retrying.
Do not run APT or another Node installer concurrently.

Global npm packages and app configurations are not deleted or reinstalled.
Bitcoin/LND binaries, data and TLS files are not changed. The helper does not
restart services, start a LOS upgrade, update container images or rebuild apps.
It leaves `needrestart` in report-only mode for this APT invocation.

**Compatibility:** a running Node service keeps its existing runtime until it
restarts. On its next start it will use the new global Node, unless explicitly
pinned elsewhere. Old ThunderHub/BOS versions and native addons may require
their own compatible update/rebuild; preserving their files is not a guarantee
that every historical version supports Node 24. Check these tools before and
after preparation. Per-user `nvm` installations are not updated by this script.

Read-only verification:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash -s -- --check
```

The script is repeatable. A failed download/checksum/simulation leaves the
installed Node unchanged. An interrupted APT transaction needs its reported
package-manager recovery before retrying. LOS application rollback does **not**
roll back global Node/npm; do not downgrade to a vulnerable runtime as a way to
repair an application upgrade. Preserve logs for support.

Supported packages: Debian/Ubuntu Linux amd64 and arm64. Standalone preparation
requires curl, CA certificates, standard coreutils and APT/dpkg; installer
prerequisite setup supplies these for new installations. Native ARM testing is
not implied by package availability.

## Português

**Os comandos abaixo só estarão disponíveis após publicar a tag 0.5.48-Beta.**

A máquina é considerada dedicada ao LOS, inclusive em `install_existing`, com
Bitcoin, LND e ferramentas opcionais como ThunderHub e Balance of Satoshis.
Instaladores e atualizadores da 0.5.48+ instalam/atualizam o **Node/npm global**,
sem uma cópia privada apenas para compilar. Instalações novas fazem isso
automaticamente.

**ATENÇÃO: quem estiver na 0.5.47 ou anterior**, antes do upgrade, execute uma
vez, sem outro upgrade em andamento. Não rode o instalador por cima do LOS:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash
```

Aguarde `[OK] Ready` e depois atualize pela UI. Se falhar, preserve o erro e
resolva a causa antes de continuar. **Versões anteriores à 0.5.40 continuam
obrigadas a passar pela 0.5.40 primeiro.**

O script instala Node **24.22.1** com npm **11.19.1** por pacote NodeSource,
verificado por SHA-256 fixado na release e instalado pelo APT. Não executa
scripts remotos de configuração, não modifica repositórios APT nem perfis do
shell. Versões futuras do LOS aplicam a política de Node/npm revisada para elas.

O plano do APT é verificado: só instala/atualiza `nodejs` e permite remover os
pacotes conflitantes de npm/documentação/headers da distribuição. Não faz
`autoremove`. Dependências ausentes, pacotes retidos, mudanças em pacotes não
relacionados, Node mais novo ou instalações manuais conflitantes interrompem
a preparação, sem tentar consertos amplos. Resolva o motivo e repita. Não
execute APT ou outro instalador Node em paralelo.

Não apaga/reinstala pacotes npm globais nem configurações de apps. Não altera
binários/dados/TLS do Bitcoin/LND, não reinicia serviços nem atualiza containers.
O `needrestart` fica somente informativo nessa execução do APT.

**Compatibilidade:** serviços Node em execução mantêm o runtime antigo até
reiniciarem. No próximo início usarão o novo Node global, salvo configuração
explícita diferente. Versões antigas do ThunderHub/BOS e módulos nativos podem
precisar de atualização/recompilação própria: preservar arquivos não garante
compatibilidade de toda versão histórica com Node 24. Confira essas ferramentas
antes e depois. Instalações `nvm` por usuário não são atualizadas.

Verificação sem alterar arquivos:

```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/0.5.48-Beta/lightningos-light/scripts/prepare-node-upgrade.sh | sudo bash -s -- --check
```

Pode repetir o script. Falhas de download/checksum/simulação não alteram o Node
instalado. Uma interrupção do APT exige tratar a recuperação indicada pelo
gerenciador de pacotes. O rollback do app LOS **não desfaz** a atualização global
do Node/npm; não volte a um runtime vulnerável para tentar resolver um upgrade.
