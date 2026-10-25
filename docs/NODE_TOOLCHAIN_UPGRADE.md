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
configured. Conflicting distro npm/development/documentation packages and their
automatically installed Node/JS dependency stack may be removed, because the
new package bundles npm. The stack is derived from installed package metadata;
manually installed extra tools and unrelated dependencies stay protected.
No autoremove is run. Missing dependencies, held packages,
unrelated removals/changes, an existing newer Node version, or a conflicting
manual global installation stop preparation rather than trigger a broad repair.
Install the reported prerequisites or resolve the conflict before retrying.
Do not run APT or another Node installer concurrently.

From the 0.5.49 preparer, a newer installed Node receives a more specific
diagnostic. Node 25 is identified as an end-of-life line according to the
[upstream schedule](https://nodejs.org/en/about/previous-releases); a larger
major number does not mean newer security fixes. Other newer versions are
reported as unvalidated, not automatically described as unsupported/insecure.
In both cases preparation still stops without replacing Node/npm or app
configurations. Review dependent apps and plan a controlled migration where
needed; there is no force-downgrade option. Successfully upgrading LOS through
an old UI updater does not patch the existing Node runtime. These diagnostics
do not change the already-published 0.5.48 script.

The refusal also reports the APT candidate from cached local metadata, when
available. It does not run apt update or expose repository URLs, and a missing
candidate does not prevent the refusal. Candidate availability is not proof of
security fixes. For Node26, review a security-fixed release in the same major
line before considering a downgrade; being patched does not mean that LOS has
validated compatibility with that version. No candidate is automatically
installed and no repositories or services are changed by these diagnostics.
After successful preparation, a notice reminds operators to plan restarts of
already-running Node apps to load the updated runtime; the preparer does not
perform those restarts.

Global npm packages and app configurations are not deleted or reinstalled.
When the old npm uses `/usr/local`, the helper preserves that global prefix in
root-owned `/usr/etc/npmrc` before replacing the distro runtime. This also keeps
future global package updates pointed at the existing packages. User npmrc files
are not edited; custom prefixes outside `/usr` and `/usr/local` require review.
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
requires Python 3, curl, CA certificates, standard coreutils and APT/dpkg; installer
prerequisite setup supplies these for new installations. Native ARM testing is
not implied by package availability.

## Recovery for the immutable 0.5.49 updater / Recuperação para a 0.5.49

The 0.5.48 updater runs the target Node preparer and requires the exact LTS pin.
Older updaters may build with the installed runtime instead, which explains why
a direct 0.5.46 → 0.5.49 upgrade can succeed with Node 26 while 0.5.48 → 0.5.49
refuses it. Neither result proves that all Node security patches were applied.

`lightningos-light/scripts/recover-node-for-0.5.49.sh` is a **separate, explicit
global migration**, not the normal compatibility check. Run `--check` first to
simulate, then `--migrate-to-lts` to consent to Node 24.22.1/npm 11.19.1. It
downloads the original 0.5.49 preparer by immutable commit and verifies its
SHA-256, verifies the pinned Node package, permits only a nodejs package change,
preserves the global prefix and package inventory, and aligns one recognized
NodeSource source to node_24.x with a private backup. No app, Bitcoin or LND
service is restarted. Other Node apps/native addons need their own compatibility
review. Unsafe files, unhealthy npm inventory, unexpected APT plans and duplicate
or unfamiliar repositories stop the operation. A failed APT transaction requires
dpkg recovery; package migration is not automatically rolled back. Root-only
records are retained under `/var/cache/lightningos-node-recovery.*`; do not post
their contents. Only after `[OK] Ready`, upgrade to the published 0.5.49 via UI.

O atualizador da 0.5.48 exige o Node LTS exato da release de destino; atualizadores
mais antigos podiam compilar com o runtime existente. Por isso o Pi4 que veio da
0.5.46 passou e o Node Privado vindo da 0.5.48 recusou o Node 26. Não é permissão.
O script separado `recover-node-for-0.5.49.sh` oferece `--check` (simulação) e
`--migrate-to-lts` (consentimento explícito para migrar o Node/npm global ao LTS
exigido). Preserva o inventário npm e o prefixo global; verifica pacote e hash;
recusa alterações de outros pacotes; alinha uma fonte NodeSource reconhecida à
linha 24 com backup privado. Não reinicia serviços. Revise outros apps Node e
addons nativos antes de migrar. Após `[OK] Ready`, atualize o LOS pela UI. Falhas
do APT podem exigir recuperação do dpkg; não há rollback automático de pacotes.

### From 0.5.50 / A partir da 0.5.50

Existing global Node **>=22.12.0** and npm **>=10.0.0** are retained when both
executables run successfully, including newer patches/majors. Missing or old
toolchains still use the verified LTS installation path; new empty installations
therefore retain the recommended pin. No automatic downgrade or repository
rewrite occurs in normal preparation. Conflicting executables and unsafe npm
configuration still fail closed. `--check` now checks build compatibility rather
than exact Debian package identity. The actual lockfile install and UI build
remain required and can fail on genuine incompatibilities.

Compatibility does **not** certify security: the preparer warns about known EOL
Node 25, versions below the reviewed 22/24/26 security baseline, or unreviewed
future majors. These warnings do not prevent a compatible LOS build. Keep the
runtime patched separately. The normal script does not restart Node applications.

Da 0.5.50 em diante, Node global **>=22.12.0** e npm **>=10.0.0** funcionais são
mantidos, sem exigir versões exatas. Ferramentas ausentes/antigas continuam pelo
caminho LTS verificado. Colisões de executáveis e configurações inseguras continuam
bloqueadas. `--check` verifica compatibilidade de build; `npm ci` e o build real
ainda precisam passar. Alertas de segurança/EOL ficam separados da compatibilidade
e não impedem o upgrade somente por diferença de versão. O texto da 0.5.48/49
abaixo é histórico e descreve os preparadores imutáveis daquelas releases.

Security baselines reviewed against the [upstream October advisory](https://nodejs.org/en/blog/vulnerability/october-2026-security-releases):
22.23.4, 24.22.1 and 26.12.1. This is not a promise to track future advisories
automatically.

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

O plano do APT é verificado: só instala/configura `nodejs`. Permite substituir
os pacotes conflitantes de npm/documentação/headers e a pilha Node/JS instalada
automaticamente como dependência deles, pois o novo pacote já inclui npm.
A lista vem dos metadados dos pacotes instalados; ferramentas extras instaladas
manualmente e dependências não relacionadas continuam protegidas. Não faz
`autoremove`. Dependências ausentes, pacotes retidos, mudanças em pacotes não
relacionados, Node mais novo ou instalações manuais conflitantes interrompem
a preparação, sem tentar consertos amplos. Resolva o motivo e repita. Não
execute APT ou outro instalador Node em paralelo.

A partir do preparador da 0.5.49, o diagnóstico diferencia Node 25, uma linha
fora de suporte conforme o [calendário oficial](https://nodejs.org/en/about/previous-releases),
de outras versões mais novas ainda não validadas por esta release. Número
maior não garante correções de segurança mais recentes. Ambos os casos continuam
interrompendo a preparação, sem substituir Node/npm ou configurações dos apps.
Revise os apps dependentes e planeje uma migração controlada quando necessária;
não há opção para forçar downgrade. Um upgrade do LOS pelo atualizador antigo
não corrige o Node existente. Essa melhoria não altera o script já publicado
da 0.5.48.

A recusa também informa a candidata do APT a partir dos metadados locais,
quando disponíveis, sem executar apt update nem expor URLs dos repositórios.
Ausência de candidata não impede o diagnóstico; ter uma candidata não comprova
correções de segurança. Para Node26, avalie uma versão corrigida na própria
linha antes de considerar downgrade. Segurança corrigida e compatibilidade
validada pelo LOS são critérios diferentes. O diagnóstico não instala a
candidata nem altera repositórios ou serviços. Após uma preparação bem-sucedida,
um aviso lembra que apps Node já em execução precisam de reinício planejado
para carregar o novo runtime; o preparador não executa esses reinícios.

Não apaga/reinstala pacotes npm globais nem configurações de apps. Preserva
o prefixo global anterior `/usr/local` em `/usr/etc/npmrc`, pertencente ao root,
antes de substituir o runtime, para que futuras atualizações encontrem os
pacotes existentes. Não edita o npmrc dos usuários; prefixos personalizados
fora de `/usr` e `/usr/local` exigem revisão. Não altera
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
